package server_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestNoThirdPartyDependencies is the §2.1 rule: go.mod must have no require
// entries, so a clean checkout builds with nothing but the Go toolchain.
func TestNoThirdPartyDependencies(t *testing.T) {
	gomod := readFile(t, filepath.Join(repoRoot(t), "go.mod"))
	for _, line := range strings.Split(gomod, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "require") {
			t.Errorf("go.mod contains a dependency block: %q", trimmed)
		}
	}
}

// TestBuildArtifactsAreIgnored stops a compiled binary from ever being committed
// (.gitignore) or shipped into the image build context (.dockerignore).
func TestBuildArtifactsAreIgnored(t *testing.T) {
	root := repoRoot(t)
	gitignore := readFile(t, filepath.Join(root, ".gitignore"))
	dockerignore := readFile(t, filepath.Join(root, ".dockerignore"))

	// The Windows binary is the one that actually gets produced here; the
	// extensionless name is what a Linux build leaves behind.
	for _, name := range []string{"/dirserve.exe", "/dirserve"} {
		if !strings.Contains(gitignore, name) {
			t.Errorf(".gitignore does not ignore %s", name)
		}
	}
	// The binary must never enter the Docker build context.
	if !strings.Contains(dockerignore, "dirserve") {
		t.Error(".dockerignore does not exclude the built binary")
	}
}

// TestComposeDoesNotBuild guards the deployment model: the stack pulls the
// published image, it does not build it locally (§12 of the project plan).
func TestComposeDoesNotBuild(t *testing.T) {
	compose := readFile(t, filepath.Join(repoRoot(t), "docker-compose.yml"))

	// Strip comments so documentation mentioning "build" cannot fail the check.
	var code strings.Builder
	for _, line := range strings.Split(compose, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code.WriteString(line)
		code.WriteByte('\n')
	}
	body := code.String()

	if regexp.MustCompile(`(?m)^\s*build\s*:`).MatchString(body) {
		t.Error("docker-compose.yml has a build: key; the stack must pull the image")
	}
	if !strings.Contains(body, "image:") {
		t.Error("docker-compose.yml does not specify an image to run")
	}
	if !strings.Contains(body, "ghcr.io/") {
		t.Error("docker-compose.yml should default to the published GHCR image")
	}
}

// TestComposeVariablesHaveDefaults makes the Portainer paste-and-run promise
// real: every ${VAR} must be ${VAR:-default}, never bare ${VAR}, which compose
// would substitute as empty.
func TestComposeVariablesHaveDefaults(t *testing.T) {
	compose := readFile(t, filepath.Join(repoRoot(t), "docker-compose.yml"))
	var code strings.Builder
	for _, line := range strings.Split(compose, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code.WriteString(line)
		code.WriteByte('\n')
	}

	interpolations := regexp.MustCompile(`\$\{([^}]*)\}`).FindAllStringSubmatch(code.String(), -1)
	if len(interpolations) == 0 {
		t.Fatal("no variables found in docker-compose.yml; expected Portainer parameters")
	}
	for _, m := range interpolations {
		spec := m[1]
		if !strings.Contains(spec, ":-") && !strings.Contains(spec, ":?") {
			t.Errorf("variable ${%s} has no default and would resolve empty", spec)
		}
	}

	// The two the user asked to control from Portainer must be present.
	for _, want := range []string{"DIRTO_SHARE", "TOKEN"} {
		if !strings.Contains(code.String(), want) {
			t.Errorf("docker-compose.yml does not expose %s", want)
		}
	}
}

// TestComposeMountsTheServedDirectoryReadOnly keeps the read-only promise at the
// deployment layer, not just in the code.
func TestComposeMountsTheServedDirectoryReadOnly(t *testing.T) {
	compose := readFile(t, filepath.Join(repoRoot(t), "docker-compose.yml"))
	// Find the volume line that targets the container's /data.
	re := regexp.MustCompile(`(?m)^\s*-\s*"\$\{DIRTO_SHARE[^}]*\}:/data(?::ro)?"`)
	m := re.FindString(compose)
	if m == "" {
		t.Fatal("no /data volume found in docker-compose.yml")
	}
	if !strings.Contains(strings.TrimSpace(m), ":/data:ro") {
		t.Errorf("/data is not mounted read-only: %s", strings.TrimSpace(m))
	}
}

// TestWorkflowRunsTestsBeforePublishing is the ordering guarantee: the image job
// must depend on the test job, so a failure can never ship a broken latest.
func TestWorkflowRunsTestsBeforePublishing(t *testing.T) {
	workflow := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))

	needsTest := regexp.MustCompile(`(?m)^\s+docker:\s*\n(?:.*\n)*?\s+needs:\s*test\b`).MatchString(workflow)
	if !needsTest {
		t.Error("the docker job does not declare needs: test")
	}
	for _, step := range []string{"go vet", "go test", "./e2e.sh", "gofmt"} {
		if !strings.Contains(workflow, step) {
			t.Errorf("ci.yml never runs %q", step)
		}
	}
}

// TestLicenseIsMIT keeps the license file present and identifiable.
func TestLicenseIsMIT(t *testing.T) {
	lic := readFile(t, filepath.Join(repoRoot(t), "LICENSE"))
	if !strings.Contains(lic, "MIT License") {
		t.Error("LICENSE does not contain the MIT license text")
	}
	if !strings.Contains(lic, "dirserve contributors") {
		t.Error("LICENSE does not carry the expected copyright holder")
	}
}

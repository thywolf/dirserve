package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dirserve/internal/fsx"
)

// fixture builds a served tree and returns a test server over it. The optional
// mods run after the default config is built and may replace the FS handle, so
// the --hidden path is exercised through the same code the binary uses.
func fixture(t *testing.T, mods ...func(*Config)) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("setup.sh", "#!/bin/sh\necho hello-from-setup\n")
	write("configs/app.conf", "port = 8080\n")
	write("api/tokens.json", "{\"k\":\"v\"}\n")
	write("healthz", "ok\n")
	write("notes.txt", "line one\nline two\n")
	write("payload.html", "<script>alert(1)</script>\n")
	write("vector.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`+"\n")
	write("my file #1.txt", "spaces and hash\n")
	write("café/naïve.txt", "unicode\n")
	write(".dotfile", "dot\n")
	write("README.md", "# readme\n")
	write("LICENSE", "MIT\n")

	cfg := Config{
		MaxPreviewBytes: 1 << 20,
		Quiet:           true,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, mod := range mods {
		mod(&cfg)
	}
	if cfg.FS == nil {
		fsys, err := fsx.Open(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fsys.Close() })
		cfg.FS = fsys
	}
	return New(cfg), dir
}

// hiddenFixture serves the same tree with dotfiles hidden, by opening the FS
// with hideDots set — the same thing --hidden does in main.
func hiddenFixture(t *testing.T) http.Handler {
	// Serve a dotfile-bearing tree with dotfiles hidden.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".dotfile"), []byte("dot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys, err := fsx.Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })
	return New(Config{
		FS:              fsys,
		MaxPreviewBytes: 1 << 20,
		Quiet:           true,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// get issues a GET with an optional Accept header.
func get(t *testing.T, h http.Handler, path, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRootIsTheHealthCheck(t *testing.T) {
	h, _ := fixture(t)
	// No Accept header at all: curl's default, the pipeable plain-text listing.
	rec := get(t, h, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"setup.sh", "api/", "configs/", "notes.txt", "healthz"} {
		if !strings.Contains(body, want) {
			t.Errorf("plain listing missing %q; got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<") {
		t.Errorf("plain listing should contain no markup; got:\n%s", body)
	}
}

func TestPlainListingIsPipeable(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/configs/", "")
	if rec.Code != 200 || rec.Body.String() != "app.conf\n" {
		t.Fatalf("configs listing = %q (status %d), want %q", rec.Body.String(), rec.Code, "app.conf\n")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestEmptyDirectoryPrintsNothing(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fsx.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	h := New(Config{FS: fsys, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	rec := get(t, h, "/", "")
	if rec.Code != 200 {
		t.Errorf("empty root status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("empty directory listing = %q, want an empty body", rec.Body.String())
	}
}

func TestJSONListing(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/", "application/json")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var listing struct {
		Path    string `json:"path"`
		Entries []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Size  int64  `json:"size"`
			Mtime int64  `json:"mtime_unix"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, rec.Body.String())
	}
	if listing.Path != "/" {
		t.Errorf("path = %q, want /", listing.Path)
	}
	if len(listing.Entries) == 0 {
		t.Fatal("no entries")
	}
	// Directories come first.
	if listing.Entries[0].Type != "dir" {
		t.Errorf("first entry %q type = %q, want dir", listing.Entries[0].Name, listing.Entries[0].Type)
	}
	var sawFile bool
	for _, e := range listing.Entries {
		if e.Type != "dir" && e.Type != "file" {
			t.Errorf("entry %q has type %q", e.Name, e.Type)
		}
		if e.Type == "file" {
			sawFile = true
			if e.Mtime == 0 {
				t.Errorf("file %q has mtime_unix 0", e.Name)
			}
		}
	}
	if !sawFile {
		t.Error("no file entries in listing")
	}
}

func TestNoReservedNamespaces(t *testing.T) {
	h, _ := fixture(t)
	// A directory named api browses like any other directory.
	rec := get(t, h, "/api/", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "tokens.json") {
		t.Errorf("/api/ = %d %q", rec.Code, rec.Body.String())
	}
	// A file named healthz downloads like any other file.
	rec = get(t, h, "/healthz", "")
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("/healthz = %d %q", rec.Code, rec.Body.String())
	}
	// The same two under the other representations.
	rec = get(t, h, "/api/", "application/json")
	if rec.Code != 200 {
		t.Errorf("/api/ as JSON = %d", rec.Code)
	}
	rec = get(t, h, "/api/", "text/html")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("/api/ as HTML = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = get(t, h, "/api/tokens.json", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"k"`) {
		t.Errorf("/api/tokens.json = %d %q", rec.Code, rec.Body.String())
	}
}

func TestFileGetExactBytes(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/setup.sh", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Body.String(); got != "#!/bin/sh\necho hello-from-setup\n" {
		t.Errorf("body = %q", got)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on a file response")
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Error("no Accept-Ranges on a file response")
	}
}

func TestContentTypePolicyOverHTTP(t *testing.T) {
	h, _ := fixture(t)
	tests := []struct {
		path string
		want string
	}{
		{"/notes.txt", "text/plain; charset=utf-8"},
		{"/configs/app.conf", "text/plain; charset=utf-8"},
		{"/setup.sh", "text/plain; charset=utf-8"},
		{"/README.md", "text/plain; charset=utf-8"},
		{"/payload.html", "application/octet-stream"},
		{"/vector.svg", "application/octet-stream"},
		{"/LICENSE", "text/plain; charset=utf-8"},
	}
	for _, tt := range tests {
		rec := get(t, h, tt.path, "")
		if got := rec.Header().Get("Content-Type"); got != tt.want {
			t.Errorf("%s Content-Type = %q, want %q", tt.path, got, tt.want)
		}
		// Every file response carries the §5.1 safety pair.
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s missing nosniff", tt.path)
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s missing sandbox CSP", tt.path)
		}
	}
}

// TestUserContentIsNeverHTML is the end-to-end version of the §2.4 rule: nothing
// in the served tree comes back as a type a browser would execute, whatever the
// client asked for.
func TestUserContentIsNeverHTML(t *testing.T) {
	h, _ := fixture(t)
	for _, p := range []string{"/payload.html", "/vector.svg"} {
		for _, accept := range []string{"text/html", "*/*", "text/html,*/*"} {
			rec := get(t, h, p, accept)
			ct := rec.Header().Get("Content-Type")
			if strings.Contains(ct, "text/html") || strings.Contains(ct, "svg") || strings.Contains(ct, "xml") {
				t.Errorf("GET %s with Accept %q → %q, which could execute", p, accept, ct)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("GET %s missing nosniff", p)
			}
		}
	}
}

func TestTraversalRejected(t *testing.T) {
	h, dir := fixture(t)
	// A file outside the served root that traversal would try to reach.
	outside := filepath.Join(filepath.Dir(dir), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	paths := []string{
		"/..%2f..%2fetc%2fpasswd",
		"/../outside-secret.txt",
		"/configs/../../outside-secret.txt",
		"/%2e%2e/outside-secret.txt",
		"/..%252foutside-secret.txt",
		"/a%00b",
		"/%0a",
	}
	for _, p := range paths {
		rec := get(t, h, p, "")
		if rec.Code < 400 {
			t.Errorf("GET %s = %d, want a 4xx", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("GET %s leaked outside content", p)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("GET %s error Content-Type = %q, want text/plain", p, ct)
		}
	}
}

func TestSymlinkEscapeNotFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs elevation on Windows")
	}
	h, dir := fixture(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	rec := get(t, h, "/escape.txt", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("symlink escape = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Error("symlink escape leaked content")
	}
}

func TestRangeRequests(t *testing.T) {
	h, _ := fixture(t)
	req := httptest.NewRequest(http.MethodGet, "/setup.sh", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := rec.Body.String(); got != "#!/bi" {
		t.Errorf("range body = %q, want %q", got, "#!/bi")
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-4/32" {
		t.Errorf("Content-Range = %q, want bytes 0-4/32", cr)
	}
}

func TestConditionalGetReturns304(t *testing.T) {
	h, _ := fixture(t)
	first := get(t, h, "/notes.txt", "")
	lastMod := first.Header().Get("Last-Modified")
	if lastMod == "" {
		t.Fatal("no Last-Modified header")
	}
	req := httptest.NewRequest(http.MethodGet, "/notes.txt", nil)
	req.Header.Set("If-Modified-Since", lastMod)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Error("304 must have an empty body")
	}
}

func TestDownloadDisposition(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/setup.sh?dl=1", "")
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment;") {
		t.Errorf("Content-Disposition = %q, want attachment", disposition)
	}
	if !strings.Contains(disposition, "setup.sh") {
		t.Errorf("disposition %q does not name the file", disposition)
	}
	// Without ?dl=1 there is no disposition.
	rec = get(t, h, "/setup.sh", "")
	if rec.Header().Get("Content-Disposition") != "" {
		t.Error("unexpected Content-Disposition on a plain GET")
	}
}

func TestAwkwardFilenamesRoundTrip(t *testing.T) {
	h, _ := fixture(t)
	for _, p := range []string{
		"/my%20file%20%231.txt",
		"/caf%C3%A9/na%C3%AFve.txt",
		"/configs/app.conf",
	} {
		rec := get(t, h, p, "")
		if rec.Code != 200 {
			t.Errorf("GET %s = %d, want 200", p, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", p)
		}
	}
	// The JSON listing must carry the raw names, not mangled ones.
	rec := get(t, h, "/", "application/json")
	if !strings.Contains(rec.Body.String(), "my file #1.txt") {
		t.Error("JSON listing did not round-trip a name with a space and #")
	}
	if !strings.Contains(rec.Body.String(), "café") {
		t.Error("JSON listing did not round-trip a unicode name")
	}
}

func TestNotFoundIsPlainText(t *testing.T) {
	h, _ := fixture(t)
	for _, p := range []string{"/nope.txt", "/nope/deeper.txt", "/configs/missing.conf"} {
		rec := get(t, h, p, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("GET %s Content-Type = %q, want text/plain", p, ct)
		}
		if strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("GET %s returned HTML for an error", p)
		}
		if body := strings.TrimSpace(rec.Body.String()); body == "" || strings.Contains(body, "\n") {
			t.Errorf("GET %s error body should be one line, got %q", p, rec.Body.String())
		}
	}
}

func TestOnlyGETAndHEAD(t *testing.T) {
	h, _ := fixture(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/setup.sh", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /setup.sh = %d, want 405", method, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s Allow = %q, method must stay read-only", method, allow)
		}
	}
}

func TestHeadMatchesGetHeaders(t *testing.T) {
	h, _ := fixture(t)
	getRec := get(t, h, "/notes.txt", "")
	req := httptest.NewRequest(http.MethodHead, "/notes.txt", nil)
	headRec := httptest.NewRecorder()
	h.ServeHTTP(headRec, req)

	if headRec.Code != getRec.Code {
		t.Errorf("HEAD status %d != GET status %d", headRec.Code, getRec.Code)
	}
	if headRec.Body.Len() != 0 {
		t.Error("HEAD returned a body")
	}
	if headRec.Header().Get("Content-Length") != getRec.Header().Get("Content-Length") {
		t.Errorf("HEAD Content-Length %q != GET %q",
			headRec.Header().Get("Content-Length"), getRec.Header().Get("Content-Length"))
	}
	if headRec.Header().Get("Content-Type") != getRec.Header().Get("Content-Type") {
		t.Error("HEAD and GET disagree on Content-Type")
	}
}

func TestUIHeadersAndCSP(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/", "text/html")
	if rec.Code != 200 {
		t.Fatalf("UI status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("UI Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("UI missing nosniff")
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("UI missing Referrer-Policy")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "style-src 'self'", "frame-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("UI CSP missing %q; got %q", want, csp)
		}
	}
	// Inline script and style are forbidden by the CSP, so the shell must link
	// them as files rather than inline them.
	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Error("shell contains an inline <script>, which the CSP would block")
	}
	if strings.Contains(body, " style=") {
		t.Error("shell contains an inline style attribute, which the CSP would block")
	}
}

func TestNoJSListing(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/configs/", "text/html")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<noscript>") {
		t.Fatal("UI response has no <noscript> fallback")
	}
	if !strings.Contains(body, "app.conf") {
		t.Error("noscript listing missing this directory's own entries")
	}
	if !strings.Contains(body, `href="/configs/app.conf"`) {
		t.Error("noscript listing should link an entry by its served path")
	}
	// A subdirectory gets a link back up.
	rec = get(t, h, "/configs/", "text/html")
	if !strings.Contains(rec.Body.String(), "parent directory") {
		t.Error("noscript listing of a subdirectory should offer the parent")
	}
}

func TestTokenAuth(t *testing.T) {
	h, _ := fixture(t, func(c *Config) { c.Token = "s3cret" })

	t.Run("rejects unauthenticated", func(t *testing.T) {
		for _, tc := range []struct{ path, accept string }{
			{"/", ""},
			{"/setup.sh", ""},
			{"/", "application/json"},
			{"/", "text/html"},
		} {
			rec := get(t, h, tc.path, tc.accept)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("unauthenticated %s (Accept %q) = %d, want 401", tc.path, tc.accept, rec.Code)
			}
		}
	})

	t.Run("accepts bearer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/setup.sh", nil)
		req.Header.Set("Authorization", "Bearer s3cret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("bearer auth = %d, want 200", rec.Code)
		}
	})

	t.Run("accepts access_token query", func(t *testing.T) {
		rec := get(t, h, "/setup.sh?access_token=s3cret", "")
		if rec.Code != 200 {
			t.Errorf("query token = %d, want 200", rec.Code)
		}
	})

	t.Run("rejects wrong token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/setup.sh", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("wrong token = %d, want 401", rec.Code)
		}
	})
}

func TestHiddenMode(t *testing.T) {
	h := hiddenFixture(t)
	rec := get(t, h, "/", "")
	if strings.Contains(rec.Body.String(), ".dotfile") {
		t.Error("hidden mode still lists a dotfile")
	}
	rec = get(t, h, "/.dotfile", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("hidden dotfile fetch = %d, want 404", rec.Code)
	}
}

func TestNegotiationTable(t *testing.T) {
	h, _ := fixture(t)
	tests := []struct {
		accept     string
		wantPrefix string
	}{
		{"", "text/plain"},
		{"*/*", "text/plain"},
		{"text/html", "text/html"},
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "text/html"},
		{"application/json", "application/json"},
		{"application/json, text/plain, */*", "application/json"},
		{"text/plain", "text/plain"},
		{"application/octet-stream", "text/plain"},
		{"image/png", "text/plain"},
	}
	for _, tt := range tests {
		rec := get(t, h, "/configs/", tt.accept)
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tt.wantPrefix) {
			t.Errorf("Accept %q → %q, want prefix %q", tt.accept, ct, tt.wantPrefix)
		}
	}
}

func TestDirectoryRedirectAddsSlash(t *testing.T) {
	h, _ := fixture(t)
	req := httptest.NewRequest(http.MethodGet, "/configs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("GET /configs = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/configs/" {
		t.Errorf("Location = %q, want /configs/", loc)
	}
}

func TestAssetsServed(t *testing.T) {
	h, _ := fixture(t)
	for path, wantType := range map[string]string{
		"/style.css": "text/css",
		"/app.js":    "text/javascript",
	} {
		rec := get(t, h, path, "")
		if rec.Code != 200 {
			t.Errorf("GET %s = %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, wantType) {
			t.Errorf("%s Content-Type = %q, want %q", path, ct, wantType)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s missing nosniff", path)
		}
	}
}

// TestServedFileBeatsEmbeddedAsset is the §2.10 guarantee that the asset paths
// are a fallback, not a namespace: a file called app.js in the served root is
// served as itself.
func TestServedFileBeatsEmbeddedAsset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('mine')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys, err := fsx.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	h := New(Config{FS: fsys, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	rec := get(t, h, "/app.js", "")
	if rec.Body.String() != "console.log('mine')\n" {
		t.Errorf("GET /app.js = %q, want the served file's own bytes", rec.Body.String())
	}
	// And a genuinely absent path still falls through to the embedded UI asset.
	rec = get(t, h, "/style.css", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "--accent") {
		t.Errorf("GET /style.css = %d, want the embedded stylesheet", rec.Code)
	}
}

func TestIsLoopback(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		"0.0.0.0:8080":   false,
		"192.168.1.5:80": false,
		":8080":          false,
	}
	for addr, want := range tests {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestClampPreviewBounds(t *testing.T) {
	if got := ClampPreviewBounds(-1); got != 0 {
		t.Errorf("ClampPreviewBounds(-1) = %d, want 0", got)
	}
	if got := ClampPreviewBounds(1 << 20); got != 1<<20 {
		t.Errorf("ClampPreviewBounds(1MiB) = %d", got)
	}
	if got := ClampPreviewBounds(1 << 40); got != 64<<20 {
		t.Errorf("ClampPreviewBounds(1GiB) = %d, want the 64MiB ceiling", got)
	}
}

func TestHumanSize(t *testing.T) {
	tests := map[int64]string{
		0:       "0 B",
		512:     "512 B",
		1024:    "1.0 KB",
		1536:    "1.5 KB",
		1 << 20: "1.0 MB",
		1 << 30: "1.0 GB",
	}
	for in, want := range tests {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

// percentFixture serves a tree where one filename can only be spelled with an
// encoded separator and one directory can only be spelled with an encoded
// percent. The old whole-path decode collapsed both onto other entries.
func percentFixture(t *testing.T) http.Handler {
	dir := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// z/f.txt and z%2ff.txt are different files with contents that say which
	// one was served.
	write("z/f.txt", "DIR-SLASH\n")
	write("z%2ff.txt", "ENCODED-SLASH\n")
	write("dir%pct/inner.txt", "PCT-DIR\n")
	write("100%.txt", "PERCENT-NAME\n")

	fsys, err := fsx.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })
	return New(Config{
		FS:              fsys,
		MaxPreviewBytes: 1 << 20,
		Quiet:           true,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// TestPercentInFilenameIsNotDoubleDecoded is the regression for a real bug:
// net/http had already decoded r.URL.Path, and DecodePath decoded it a second
// time, so "z%252ff.txt" (the only correct spelling of the file z%2ff.txt)
// collapsed onto the directory z/f.txt and served the wrong bytes.
func TestPercentInFilenameIsNotDoubleDecoded(t *testing.T) {
	h := percentFixture(t)
	rec := get(t, h, "/z%252ff.txt", "")
	if rec.Code != 200 {
		t.Fatalf("GET /z%%252ff.txt = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "ENCODED-SLASH\n" {
		t.Errorf("GET /z%%252ff.txt served %q, want the z%%2ff.txt bytes", got)
	}
	// The plain path must still be the plain path.
	rec = get(t, h, "/z/f.txt", "")
	if got := rec.Body.String(); got != "DIR-SLASH\n" {
		t.Errorf("GET /z/f.txt served %q, want the z/f.txt bytes", got)
	}
	// A directory whose name contains a percent is reachable at its one
	// correct URL, and rejects a doubled one.
	if rec := get(t, h, "/dir%25pct/", ""); rec.Code != 200 {
		t.Errorf("GET /dir%%25pct/ = %d, want 200", rec.Code)
	}
	// The doubled spelling names a different entry ("dir%25pct"), which is a
	// 404 rather than a second route onto the same directory.
	if rec := get(t, h, "/dir%2525pct/", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /dir%%2525pct/ = %d, want 404", rec.Code)
	}
	rec = get(t, h, "/100%25.txt", "")
	if got := rec.Body.String(); got != "PERCENT-NAME\n" {
		t.Errorf("GET /100%%25.txt served %q, want PERCENT-NAME", got)
	}
}

// TestEncodedSlashInSegmentRejected keeps a filename from being spelled two
// ways: "%2f" cannot become a separator, because the two-segment path it would
// produce is a different file.
func TestEncodedSlashInSegmentRejected(t *testing.T) {
	h := percentFixture(t)
	for _, p := range []string{"/z%2ff.txt", "/%2e%2e/etc/passwd", "/..%2f..%2fetc%2fpasswd", "/%2e%2e/%2e%2e/etc"} {
		if rec := get(t, h, p, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", p, rec.Code)
		}
	}
}

// TestDirectoryRedirectIsEscaped is the regression for the second real bug:
// redirectToSlash built its Location from the decoded path, so a directory
// called "my dir" redirected to a URL a client cannot resolve, and "d#h"
// produced a Location whose fragment swallowed the token behind it.
func TestDirectoryRedirectIsEscaped(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/caf%C3%A9", "")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/caf%C3%A9/" {
		t.Errorf("Location = %q, want the escaped /caf%%C3%%A9/", loc)
	}
	// A raw space or a bare # in a Location makes a redirect that no client
	// can follow. Assert the header contains no character that would break it.
	for _, p := range []string{"/caf%C3%A9", "/configs"} {
		rec := get(t, h, p, "")
		loc := rec.Header().Get("Location")
		if strings.ContainsAny(loc, " #\"") {
			t.Errorf("GET %s Location = %q contains a character a client cannot resolve", p, loc)
		}
	}
}

// TestUIPagesRefuseFraming pins frame-ancestors on the page the program
// authors, so a hostile site cannot iframe the file browser.
func TestUIPagesRefuseFraming(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/", "text/html")
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("UI CSP = %q, want frame-ancestors 'none'", csp)
	}
}

// TestServedFilesAreNotStored and TestListingsVaryOnAccept keep a token-protected
// response out of a shared cache and stop a cache handing the HTML page to a
// JSON client.
func TestServedFilesAreNotStored(t *testing.T) {
	h, _ := fixture(t)
	rec := get(t, h, "/setup.sh", "")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("file Cache-Control = %q, want no-store", cc)
	}
}

func TestListingsVaryOnAccept(t *testing.T) {
	h, _ := fixture(t)
	for _, accept := range []string{"", "application/json", "text/html"} {
		rec := get(t, h, "/", accept)
		if v := rec.Header().Get("Vary"); v != "Accept" {
			t.Errorf("Accept %q: Vary = %q, want Accept", accept, v)
		}
	}
}

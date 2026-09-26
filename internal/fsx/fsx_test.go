package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDecodePath(t *testing.T) {
	tests := []struct {
		name    string
		urlPath string
		want    string
		wantErr bool
	}{
		{"root", "/", "", false},
		{"simple", "/setup.sh", "setup.sh", false},
		{"nested", "/configs/app.conf", "configs/app.conf", false},
		{"traversal", "/a/../../etc/passwd", "", true},
		{"encoded traversal", "/..%2f..%2fetc%2fpasswd", "", true},
		{"encoded dots", "/%2e%2e/%2e%2e/etc", "", true},
		{"double slash", "/a//b", "", true},
		{"dot segment", "/a/./b", "", true},
		{"encoded newline", "/a%0ab", "", true},
		{"raw newline", "/a\nb", "", true},
		{"nul byte", "/a%00b", "", true},
		{"space in name", "/my file.txt", "my file.txt", false},
		{"unicode name", "/café/naïve.txt", "café/naïve.txt", false},
		{"emoji name", "/🎉/party.txt", "🎉/party.txt", false},
		{"hash name", "/a#b.txt", "a#b.txt", false},
		{"question name", "/a?b.txt", "a?b.txt", false},
		{"percent literal", "/100%25.txt", "100%.txt", false},
		{"leading dash", "/-rf", "-rf", false},
		{"bad escape", "/a%zz", "", true},
		{"not absolute", "relative", "", true},
		{"trailing slash is the directory form", "/a/", "a", false},
		{"doubled leading slash is still the root", "//", "", false},
		{"interior double slash", "/a//b", "", true},
		{"api dir is ordinary", "/api", "api", false},
		{"healthz file is ordinary", "/healthz", "healthz", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodePath(tt.urlPath)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodePath(%q) = %q, want error", tt.urlPath, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodePath(%q) error: %v", tt.urlPath, err)
			}
			if got != tt.want {
				t.Errorf("DecodePath(%q) = %q, want %q", tt.urlPath, got, tt.want)
			}
		})
	}
}

func TestIsText(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want bool
	}{
		{"empty", nil, true},
		{"ascii", []byte("hello\n"), true},
		{"utf8", []byte("héllo 🎉\n"), true},
		{"tabs and crlf", []byte("a\tb\r\nc\x0c"), true},
		{"nul byte", []byte("a\x00b"), false},
		{"esc control", []byte("a\x1bb"), false},
		{"invalid utf8", []byte{0xff, 0xfe, 0x00}, false},
		{"png magic", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsText(tt.in); got != tt.want {
				t.Errorf("IsText(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestContentType(t *testing.T) {
	tests := []struct {
		name      string
		sniffText bool
		want      string
	}{
		// Known text extensions.
		{"notes.txt", false, "text/plain; charset=utf-8"},
		{"README.md", false, "text/plain; charset=utf-8"},
		{"setup.sh", false, "text/plain; charset=utf-8"},
		{"main.go", false, "text/plain; charset=utf-8"},
		{"app.json", false, "text/plain; charset=utf-8"},
		{"conf.yaml", false, "text/plain; charset=utf-8"},
		{"conf.yml", false, "text/plain; charset=utf-8"},
		{"conf.toml", false, "text/plain; charset=utf-8"},
		{"site.css", false, "text/plain; charset=utf-8"},
		{"debug.log", false, "text/plain; charset=utf-8"},
		{"nginx.conf", false, "text/plain; charset=utf-8"},
		{"data.csv", false, "text/plain; charset=utf-8"},
		{"script.TS", false, "text/plain; charset=utf-8"},
		// Case insensitivity.
		{"SHOUT.MD", false, "text/plain; charset=utf-8"},
		{"PIC.PNG", false, "image/png"},
		// Inert document types: every variant of html/htm/svg/xhtml and the
		// other browser-parseable formats fall back to octet-stream (§5.1).
		{"payload.html", false, octetStream},
		{"payload.htm", false, octetStream},
		{"payload.xhtml", false, octetStream},
		{"payload.HTML", false, octetStream},
		{"vector.svg", false, octetStream},
		{"vector.SVGZ", false, octetStream},
		{"data.xml", false, octetStream},
		{"feed.rss", false, octetStream},
		{"legacy.shtml", false, octetStream},
		{"page.php", false, octetStream},
		{"tmpl.mustache", false, octetStream},
		// A real media or archive extension keeps its honest type even when an
		// inert one hides behind it: the browser is told image/png or
		// text/plain, both inert under nosniff.
		{"payload.html.txt", false, "text/plain; charset=utf-8"},
		{"x.svg.png", false, "image/png"},
		{"report.htm.old", false, octetStream},
		// Real media types so the UI can preview them.
		{"photo.jpg", false, "image/jpeg"},
		{"photo.jpeg", false, "image/jpeg"},
		{"anim.gif", false, "image/gif"},
		{"modern.webp", false, "image/webp"},
		{"doc.pdf", false, "application/pdf"},
		{"clip.mp4", false, "video/mp4"},
		{"clip.webm", false, "video/webm"},
		{"song.mp3", false, "audio/mpeg"},
		{"song.flac", false, "audio/flac"},
		// Unknown extensions are never sniffed.
		{"mystery.dat", true, octetStream},
		{"archive.zip", true, "application/zip"},
		// Extensionless: sniffed from the head.
		{"LICENSE", true, "text/plain; charset=utf-8"},
		{"LICENSE", false, octetStream},
		// Dots elsewhere in the name do not confuse the extension.
		{"plain.name.txt", false, "text/plain; charset=utf-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContentType(tt.name, tt.sniffText); got != tt.want {
				t.Errorf("ContentType(%q, %v) = %q, want %q", tt.name, tt.sniffText, got, tt.want)
			}
		})
	}
}

// TestContentTypeNeverHTML is the blanket §2.4 assertion: for a range of
// hostile names, nothing a browser could execute ever comes back.
func TestContentTypeNeverHTML(t *testing.T) {
	names := []string{
		"a.html", "a.htm", "a.xhtml", "a.svg", "a.xml", "a.php", "a.jsp",
		"A.HTML", "a.HtM", "x.svg", "index.html.bak", "widget.xsl",
		"feed.atom", "legacy.shtml", "report.HTM.old", "tmpl.hbs",
	}
	for _, name := range names {
		got := ContentType(name, true)
		if strings.Contains(got, "html") || strings.Contains(got, "svg") || strings.Contains(got, "xml") {
			t.Errorf("ContentType(%q) = %q, which a browser could execute", name, got)
		}
		if got != octetStream {
			t.Errorf("ContentType(%q) = %q, want %q", name, got, octetStream)
		}
	}
}

func TestSortEntries(t *testing.T) {
	entries := []Entry{
		{Name: "zebra.txt"},
		{Name: "Apple"},
		{Name: "banana", IsDir: true},
		{Name: "Banana", IsDir: true},
		{Name: "alpha.txt"},
		{Name: "Beta", IsDir: true},
	}
	SortEntries(entries)
	var got []string
	for _, e := range entries {
		if e.IsDir {
			got = append(got, e.Name+"/")
		} else {
			got = append(got, e.Name)
		}
	}
	// Directories first, then case-insensitive bytewise, with a bytewise
	// tiebreak so names differing only in case keep a total order.
	want := []string{"Banana/", "banana/", "Beta/", "alpha.txt", "Apple", "zebra.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("SortEntries = %v, want %v", got, want)
	}
}

// newTestFS builds a small tree and returns a rooted FS over it.
func newTestFS(t *testing.T, hideDots bool) (*FS, string) {
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
	write("setup.sh", "#!/bin/sh\necho hi\n")
	write("configs/app.conf", "port = 8080\n")
	write(".hidden", "secret\n")
	write(".dotdir/inside.txt", "x\n")
	write("notes.txt", "hello\n")
	fsys, err := Open(dir, hideDots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })
	return fsys, dir
}

func TestReadDirSortsAndHides(t *testing.T) {
	t.Run("dotfiles shown by default", func(t *testing.T) {
		fsys, _ := newTestFS(t, false)
		entries, truncated, err := fsys.ReadDir("", 0)
		if err != nil {
			t.Fatal(err)
		}
		if truncated {
			t.Error("small directory reported truncated")
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name)
		}
		// Directories first: .dotdir, configs. Then files: .hidden, notes.txt,
		// setup.sh — all case-insensitive bytewise.
		want := []string{".dotdir", "configs", ".hidden", "notes.txt", "setup.sh"}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("ReadDir = %v, want %v", names, want)
		}
	})
	t.Run("dotfiles hidden", func(t *testing.T) {
		fsys, _ := newTestFS(t, true)
		entries, _, err := fsys.ReadDir("", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name, ".") {
				t.Errorf("hidden mode still listed %q", e.Name)
			}
		}
		// configs/ and notes.txt and setup.sh survive; both dotfiles are gone.
		if len(entries) != 3 {
			t.Errorf("got %d entries, want 3 (configs, notes.txt, setup.sh)", len(entries))
		}
	})
}

func TestReadDirTruncation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fsys, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	entries, truncated, err := fsys.ReadDir("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Errorf("got %d entries, want the 10-entry cap", len(entries))
	}
	if !truncated {
		t.Error("truncated = false, want true for a capped listing")
	}

	_, truncated, err = fsys.ReadDir("", 100)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("truncated = true for a listing under the cap")
	}
}

func TestConfineSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs elevation on Windows")
	}
	fsys, dir := newTestFS(t, false)

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	// A symlinked directory pointing out of the root must fail the same way.
	if err := os.Symlink(outside, filepath.Join(dir, "escape-dir")); err != nil {
		t.Fatal(err)
	}
	// An in-root symlink is legal and must resolve normally.
	if err := os.Symlink(filepath.Join(dir, "notes.txt"), filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := fsys.OpenFile("escape.txt"); err == nil {
		t.Error("OpenFile(symlink out of root) succeeded, want error")
	}
	if _, _, err := fsys.OpenFile("escape-dir/secret.txt"); err == nil {
		t.Error("OpenFile through an escaping symlinked dir succeeded, want error")
	}
	// The escaping symlink is not listed, because its target cannot be stat'ed
	// through the root either.
	entries, _, err := fsys.ReadDir("", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "escape") {
			t.Errorf("listing exposed escaping symlink %q", e.Name)
		}
	}
	f, _, err := fsys.OpenFile("alias.txt")
	if err != nil {
		t.Fatalf("in-root symlink should resolve: %v", err)
	}
	f.Close()
}

func TestOpenFileInsideRoot(t *testing.T) {
	fsys, _ := newTestFS(t, false)
	f, info, err := fsys.OpenFile("configs/app.conf")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if info.Size() != int64(len("port = 8080\n")) {
		t.Errorf("size = %d", info.Size())
	}
}

func TestHiddenBlocksDotfiles(t *testing.T) {
	hidden, _ := newTestFS(t, true)
	if !hidden.Hidden(".hidden") {
		t.Error(".hidden should be hidden")
	}
	if !hidden.Hidden(".dotdir/inside.txt") {
		t.Error("contents of a hidden dot-directory should be hidden")
	}
	if hidden.Hidden("notes.txt") {
		t.Error("a normal name should not be hidden")
	}

	shown, _ := newTestFS(t, false)
	if shown.Hidden(".hidden") {
		t.Error("dotfiles must be visible by default")
	}
}

func TestOpenRejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(file, false); err == nil {
		t.Error("Open on a file should fail: the root must be a directory")
	}
	if _, err := Open(filepath.Join(dir, "missing"), false); err == nil {
		t.Error("Open on a missing path should fail")
	}
}

func TestOpenMissingIsNotFound(t *testing.T) {
	fsys, _ := newTestFS(t, false)
	if _, _, err := fsys.OpenFile("nope.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenFile(missing) error = %v, want ErrNotFound", err)
	}
}

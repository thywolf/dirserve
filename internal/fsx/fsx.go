// Package fsx is dirserve's single filesystem chokepoint.
//
// Every read of the served directory goes through one *os.Root opened on that
// directory, so a request can never reach outside it: no "..", no absolute path,
// no symlink whose target leaves the root (os.Root resolves symlinks that stay
// inside and refuses the ones that don't). Paths handed to FS methods are
// already percent-decoded and validated by DecodePath.
package fsx

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Errors callers can branch on. A path that is merely absent is reported as
// ErrNotFound whether it never existed, is hidden by configuration, or is a
// symlink escaping the root: the distinction is not useful to a client.
var (
	// ErrUnsafePath is a request path that failed validation (see DecodePath).
	ErrUnsafePath = errors.New("unsafe path")
	// ErrNotFound means the path does not resolve to a servable entry.
	ErrNotFound = errors.New("not found")
)

// Entry is one row of a directory listing.
type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// FS is a read-only handle on the served directory.
type FS struct {
	root     *os.Root
	absRoot  string
	hideDots bool
}

// Open roots the served directory. It fails if dir is missing or is not a
// directory, so the server can refuse to start rather than serve a surprise.
func Open(dir string, hideDots bool) (*FS, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", abs)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	return &FS{root: root, absRoot: abs, hideDots: hideDots}, nil
}

func (f *FS) Close() error { return f.root.Close() }

// AbsRoot is the resolved absolute path, for the startup line and the UI.
func (f *FS) AbsRoot() string { return f.absRoot }

// HideDots reports whether dotfiles are hidden (and unservable).
func (f *FS) HideDots() bool { return f.hideDots }

// rel converts a root-relative path to the form os.Root wants ("." is the root).
func rel(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// Hidden reports whether path starts with a dotfile that configuration hides.
// The whole path is checked so a hidden dot-directory hides its contents too.
func (f *FS) Hidden(path string) bool {
	if !f.hideDots {
		return false
	}
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// Stat returns metadata for a root-relative path.
func (f *FS) Stat(path string) (os.FileInfo, error) {
	return f.root.Stat(rel(path))
}

// OpenFile opens a root-relative path for reading, together with its metadata.
func (f *FS) OpenFile(path string) (*os.File, os.FileInfo, error) {
	file, err := f.root.Open(rel(path))
	if err != nil {
		return nil, nil, wrap(err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, wrap(err)
	}
	return file, info, nil
}

// ReadDir lists one directory, sorted directories-first then case-insensitive
// bytewise, capped at limit entries (limit <= 0 means no cap). The bool result
// reports that entries were dropped. Entries that vanish or cannot be stat'ed
// mid-listing are skipped rather than failing the whole listing.
func (f *FS) ReadDir(path string, limit int) ([]Entry, bool, error) {
	dir, err := f.root.Open(rel(path))
	if err != nil {
		return nil, false, wrap(err)
	}
	defer dir.Close()

	// ReadDir(-1) streams the directory in batches, so a huge directory costs
	// one entry slice at a time instead of the whole listing up front.
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, false, wrap(err)
	}
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		if f.hideDots && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := f.root.Stat(join(path, name))
		if err != nil {
			continue // raced with a delete, or an escaping symlink: not servable
		}
		entries = append(entries, Entry{
			Name:    name,
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	SortEntries(entries)
	if limit > 0 && len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// SortEntries orders a listing: directories first, then case-insensitive
// bytewise, with a bytewise tiebreak so names differing only in case keep a
// stable, total order.
func SortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
}

// IsText reports whether b reads as UTF-8 text. A NUL byte, invalid UTF-8 or a
// control character other than common whitespace means binary; both the content
// type policy and the preview depend on this call being conservative, because
// getting it wrong hands the browser something it might execute.
func IsText(b []byte) bool {
	for _, c := range b {
		if c == 0x7f || (c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f' && c != '\v') {
			return false
		}
	}
	return utf8.Valid(b)
}

// IsTextFile reports whether a file of this size should be treated as text,
// peeking at head via f (which the caller seeks back afterwards).
func IsTextFile(f io.ReaderAt, head []byte) bool {
	if cap(head) == 0 {
		return false
	}
	n, err := f.ReadAt(head, 0)
	if n == 0 && err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return IsText(head[:n])
}

func join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// wrap normalizes os.Root failures so callers can branch on fs.ErrNotExist.
func wrap(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

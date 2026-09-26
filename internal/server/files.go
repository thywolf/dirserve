package server

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"dirserve/internal/fsx"
)

// serveFile streams one file's bytes (§5.1).
//
// http.ServeContent over an *os.File gives Range, If-Modified-Since, ETag
// revalidation and 206/304 handling for free, and it streams: a 3 GB video is
// never read into memory, so a slow client cannot make the server buffer it.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, rel string, info os.FileInfo) {
	if !info.Mode().IsRegular() {
		// A device, socket or FIFO is not something to stream to a browser.
		writeError(w, r, http.StatusBadRequest, "not a regular file")
		return
	}
	f, _, err := s.cfg.FS.OpenFile(rel)
	if err != nil {
		s.writeOpenError(w, r, err)
		return
	}
	defer f.Close()

	name := path.Base(rel)
	// Content type from the name; only an extensionless name is sniffed, and
	// the sniff reads the head and leaves the file offset at zero for
	// ServeContent's seek-based reads.
	contentType := fsx.ContentType(name, false)
	if path.Ext(name) == "" {
		head := make([]byte, sniffLen)
		n, err := f.ReadAt(head, 0)
		if err != nil && !errors.Is(err, io.EOF) {
			s.writeOpenError(w, r, err)
			return
		}
		if fsx.IsText(head[:n]) {
			contentType = "text/plain; charset=utf-8"
		}
	}

	serveSecurityHeaders(w)
	if isDownload(r) {
		w.Header().Set("Content-Disposition", contentDisposition(name))
	}
	// size+mtime is exact enough for a read-only server and makes conditional
	// revalidation a single cheap comparison.
	w.Header().Set("ETag", fmt.Sprintf(`"%d-%d"`, info.Size(), info.ModTime().Unix()))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentType)
	// ServeContent sets Last-Modified and handles If-Range, If-Modified-Since
	// and multi-range; Content-Length and the 200/206/304 statuses come with it.
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// isDownload reports whether the caller asked for a forced download (§5.1).
func isDownload(r *http.Request) bool {
	return r.URL.Query().Get("dl") == "1"
}

// contentDisposition builds the attachment header. mime.FormatMediaType emits a
// filename* form for non-ASCII names, so unicode and emoji survive the round trip.
func contentDisposition(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// writeOpenError maps a filesystem error to a plain-text response (§5.1):
// always a one-line reason, never HTML, so curl sees something useful.
func (s *Server) writeOpenError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, fsx.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	s.cfg.Logger.Error("open failed", "path", r.URL.Path, "err", err)
	writeError(w, r, http.StatusInternalServerError, "cannot read file")
}

// writeError emits a plain-text one-line error. Errors are never HTML: a curl
// client must be able to read the reason, and an attacker-controlled filename
// must never reach an HTML context. The reason is always a fixed string.
func writeError(w http.ResponseWriter, r *http.Request, status int, reason string) {
	serveSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	io.WriteString(w, reason+"\n")
}

// pathEscapeSegments escapes each segment of a root-relative path for use in a
// URL. Segments are escaped individually so an encoded "/" inside a name cannot
// turn into a separator, and so spaces, "#", "?" and unicode round-trip to the
// same file.
func pathEscapeSegments(rel string) string {
	if rel == "" {
		return "/"
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return "/" + strings.Join(parts, "/")
}

// humanSize renders a byte count the way a human reads it, for the UI's header
// row and the no-JS listing.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	sizes := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), sizes[exp])
}

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"dirserve/internal/fsx"
)

// jsonEntry is one row of the JSON listing (§6). The field names are the
// contract the UI's app.js fetches, so they are fixed: name, type, size and
// mtime_unix. No absolute paths, no host details.
type jsonEntry struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	MtimeUnix int64  `json:"mtime_unix"`
}

type jsonListing struct {
	Path      string      `json:"path"`
	Entries   []jsonEntry `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

type jsonError struct {
	Error string `json:"error"`
}

// serveDirectory answers a directory URL by content negotiation (§5.2). One URL
// is the UI page, the JSON the UI's script reads, and a pipeable plain-text
// listing; there is no /api and no other path that changes which one you get.
func (s *Server) serveDirectory(w http.ResponseWriter, r *http.Request, rel string) {
	entries, truncated, err := s.cfg.FS.ReadDir(rel, MaxListingEntries)
	if err != nil {
		s.cfg.Logger.Error("readdir failed", "path", rel, "err", err)
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	switch negotiate(r) {
	case formatJSON:
		s.writeJSON(w, r, rel, entries, truncated)
	case formatHTML:
		s.writeUI(w, r, rel, entries, truncated)
	default:
		s.writeText(w, r, entries)
	}
}

// format is one of the three representations of a directory.
type format int

const (
	formatText format = iota
	formatJSON
	formatHTML
)

// negotiate maps the Accept header to a representation. JSON wins over HTML so a
// client that accepts both gets data rather than a page; plain text is the
// fallback that keeps `curl host/dir/ | grep setup` working with no headers.
func negotiate(r *http.Request) format {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return formatText
	}
	if accepts(accept, "application/json") {
		return formatJSON
	}
	if accepts(accept, "text/html") {
		return formatHTML
	}
	return formatText
}

// accepts reports whether an Accept list names a media type, ignoring
// q-values. Matching the bare type is what both browsers and curl mean by it.
func accepts(accept, want string) bool {
	for _, part := range strings.Split(accept, ",") {
		media := strings.TrimSpace(part)
		if i := strings.IndexByte(media, ';'); i >= 0 {
			media = strings.TrimSpace(media[:i])
		}
		if media == want {
			return true
		}
	}
	return false
}

// writeJSON emits the §6 shape. An error on a JSON client is JSON too, so the
// UI can read the reason instead of guessing the format.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, rel string, entries []fsx.Entry, truncated bool) {
	listing := jsonListing{
		Path:      "/" + rel,
		Entries:   make([]jsonEntry, 0, len(entries)),
		Truncated: truncated,
	}
	if rel == "" {
		listing.Path = "/"
	}
	for _, e := range entries {
		kind := "file"
		if e.IsDir {
			kind = "dir"
		}
		listing.Entries = append(listing.Entries, jsonEntry{
			Name:      e.Name,
			Type:      kind,
			Size:      e.Size,
			MtimeUnix: e.ModTime.Unix(),
		})
	}
	s.writeJSONBody(w, r, http.StatusOK, listing)
}

func (s *Server) writeJSONBody(w http.ResponseWriter, r *http.Request, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		s.cfg.Logger.Error("marshal failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	body = append(body, '\n')
	setSecurityHeaders(w)
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	// One URL has three representations, chosen by Accept. Without Vary a
	// shared cache would hand the HTML page to a client that asked for JSON.
	h.Set("Vary", "Accept")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// writeText emits the pipeable listing (§5.2): one name per line, directories
// suffixed with "/", directories first then case-insensitive bytewise. Nothing
// else on the page, so `curl host/scripts/ | grep setup` yields names and only
// names. An empty directory prints nothing and still returns 200.
func (s *Server) writeText(w http.ResponseWriter, r *http.Request, entries []fsx.Entry) {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(entryName(e))
		b.WriteByte('\n')
	}
	body := b.String()
	serveSecurityHeaders(w)
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	// Same three-representations-one-URL rule as the JSON listing above.
	h.Set("Vary", "Accept")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// entryName renders one listing name for the text listing: directories get a
// trailing slash so a script can tell them apart from files without stat-ing.
func entryName(e fsx.Entry) string {
	if e.IsDir {
		return e.Name + "/"
	}
	return e.Name
}

package server

import (
	"bytes"
	"html/template"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"dirserve/assets"
	"dirserve/internal/fsx"
)

// embeddedAssets are the only fixed URLs in the program, and they are not a
// reserved namespace: a request for one of these paths is resolved against the
// served tree first, so a file named app.js in the served root is served as
// itself and only a genuine miss falls through to the copy that makes the UI load.
var embeddedAssets = map[string]struct {
	body        []byte
	contentType string
}{
	"/style.css":   {assets.StyleCSS, "text/css; charset=utf-8"},
	"/app.js":      {assets.AppJS, "text/javascript; charset=utf-8"},
	"/favicon.svg": {assets.FaviconSVG, "image/svg+xml"},
}

// assetETag versions the embedded assets so a rebuilt binary busts a stale
// browser cache without any build counter. It is derived from the bytes.
var assetETag = `"` + strconv.FormatUint(fnv64(assets.StyleCSS)*31+fnv64(assets.AppJS), 36) + `"`

func fnv64(b []byte) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime
	}
	return h
}

// shellTemplate renders the single page. It is a template rather than a
// concatenated string so the served root, the preview cap and the no-JS listing
// are filled in per request, and so html/template escapes every interpolation:
// filenames are attacker-controlled and never reach raw HTML.
var shellTemplate = template.Must(template.New("shell").Parse(string(assets.IndexHTML)))

// shellData is what one page render needs.
type shellData struct {
	Root       string
	MaxPreview int64
	Path       string
	ParentHref string
	Entries    []shellEntry
	Truncated  bool
	HasParent  bool
}

// shellEntry is one row of the no-JS listing: a display name plus an href that
// decodes back to exactly that name.
type shellEntry struct {
	Name string
	Href string
	Size string
	Time string
	Dir  bool
}

// serveAsset serves an embedded asset after the served tree has failed to
// resolve the path, so a served file always wins over the embedded copy. The
// boolean reports whether an asset was served at all.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request) bool {
	asset, ok := embeddedAssets[r.URL.Path]
	if !ok {
		return false
	}
	rel := strings.TrimPrefix(r.URL.Path, "/")
	if !s.cfg.FS.Hidden(rel) {
		if info, err := s.cfg.FS.Stat(rel); err == nil {
			if !info.IsDir() {
				s.serveFile(w, r, rel, info)
				return true
			}
			return false
		}
	}
	setSecurityHeaders(w)
	h := w.Header()
	h.Set("Content-Type", asset.contentType)
	h.Set("ETag", assetETag)
	h.Set("Cache-Control", "public, max-age=300, must-revalidate")
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(asset.body))
	return true
}

// writeUI renders the single page (§7). The same response is the zero-JS
// fallback: with scripting disabled the <noscript> block in the shell is a
// plain HTML listing of this directory with working links, because the §5.2 HTML
// response doubles as the no-JS UI.
func (s *Server) writeUI(w http.ResponseWriter, r *http.Request, rel string, entries []fsx.Entry, truncated bool) {
	data := shellData{
		Root:       s.cfg.FS.AbsRoot(),
		MaxPreview: ClampPreviewBounds(s.cfg.MaxPreviewBytes),
		Path:       rel,
		Truncated:  truncated,
	}
	if rel != "" {
		data.HasParent = true
		if i := strings.LastIndexByte(rel, '/'); i > 0 {
			data.ParentHref = pathEscapeSegments(rel[:i]) + "/"
		} else {
			data.ParentHref = "/"
		}
	}
	for _, e := range entries {
		data.Entries = append(data.Entries, shellEntry{
			Name: e.Name,
			Href: pathEscapeSegments(joinRel(rel, e.Name)) + dirSuffix(e.IsDir),
			Size: humanSize(e.Size),
			Time: e.ModTime.Local().Format("2006-01-02 15:04"),
			Dir:  e.IsDir,
		})
	}

	var buf bytes.Buffer
	if err := shellTemplate.Execute(&buf, data); err != nil {
		s.cfg.Logger.Error("template failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	body := buf.Bytes()
	setSecurityHeaders(w)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// joinRel appends one entry name to a directory path; the root is "".
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
func dirSuffix(isDir bool) string {
	if isDir {
		return "/"
	}
	return ""
}

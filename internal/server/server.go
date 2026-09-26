// Package server implements dirserve's HTTP surface.
//
// There are no reserved paths. One handler serves every URL and what you get
// depends only on the filesystem plus content negotiation (Accept) and the
// single ?dl=1 query parameter. A directory named "api" is a directory like any
// other, and a file named "healthz" is a file like any other.
package server

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"dirserve/internal/fsx"
)

// MaxListingEntries caps one directory listing (§6): a directory with more
// entries returns the first 5,000 plus "truncated": true. It bounds memory for
// an accidental million-file directory without hiding normal ones.
const MaxListingEntries = 5000

// sniffLen is how much of a file head is read to classify an extensionless file
// as text or binary.
const sniffLen = 4096

// Config is the resolved runtime configuration.
type Config struct {
	FS              *fsx.FS
	Token           string
	MaxPreviewBytes int64
	Quiet           bool
	Logger          *slog.Logger
}

// Server serves one rooted filesystem.
type Server struct {
	cfg Config
}

// New builds the server. The filesystem must already be rooted and validated.
func New(cfg Config) *Server { return &Server{cfg: cfg} }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	if r.Method == http.MethodHead {
		// ServeContent understands HEAD; the writer drops the body so the
		// headers (Content-Length, Accept-Ranges, ETag) stay identical to GET.
		rec.ResponseWriter = headOnly{rec.ResponseWriter}
	}
	if !s.authenticated(r) {
		s.cfg.Logger.Warn("rejected unauthorized request", "path", r.URL.Path)
		rec.Header().Set("WWW-Authenticate", `Bearer realm="dirserve"`)
		writeError(rec, r, http.StatusUnauthorized, "missing or invalid token")
		s.log(r, rec, start)
		return
	}
	s.dispatch(rec, r)
	s.log(r, rec, start)
}

func (s *Server) log(r *http.Request, rec *statusRecorder, start time.Time) {
	if s.cfg.Quiet {
		return
	}
	s.cfg.Logger.Info("request",
		"method", r.Method,
		"path", r.URL.Path,
		"status", rec.status,
		"ms", time.Since(start).Milliseconds(),
	)
}

// dispatch is the whole routing table. There is no mux: every path means "this
// entry in the served root", which is what makes reserved namespaces impossible
// by construction rather than by discipline.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Read-only by construction: the only verbs that exist are GET and HEAD.
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "only GET and HEAD are supported")
		return
	}
	// Embedded assets are served only under their exact versioned names, and
	// only to a UI that already loaded, so a served file called app.js stays
	// reachable at its own clean path and user content is never loaded as the
	// UI's script.
	if s.serveAsset(w, r) {
		return
	}

	// EscapedPath, not Path: net/http has already percent-decoded Path, and
	// decoding it again is what used to make a file named "a%2fb.txt"
	// unreachable and alias it onto the directory "a/b.txt".
	rel, err := fsx.DecodePath(r.URL.EscapedPath())
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "unsafe path")
		return
	}
	if s.cfg.FS.Hidden(rel) {
		writeError(w, r, http.StatusNotFound, "not found")
		return
	}
	info, err := s.cfg.FS.Stat(rel)
	switch {
	case err == nil && info.IsDir():
		if !strings.HasSuffix(r.URL.EscapedPath(), "/") {
			// Canonicalize so the directory's own relative URLs and links
			// resolve against the directory rather than against its parent.
			redirectToSlash(w, r)
			return
		}
		s.serveDirectory(w, r, rel)
	case err == nil:
		s.serveFile(w, r, rel, info)
	case errors.Is(err, fsx.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not found")
	default:
		// A path that fails to resolve because it leaves the root is a
		// confinement event, not a missing file, and a server operator
		// scanning for the latter should not drown in the former. A path
		// that fails because the served directory was unmounted or replaced
		// underneath us (§11) is worth a line, at a level that does not cry
		// wolf on every other one.
		s.cfg.Logger.Warn("path not resolvable", "path", rel, "err", err)
		writeError(w, r, http.StatusNotFound, "not found")
	}
}

func redirectToSlash(w http.ResponseWriter, r *http.Request) {
	// EscapedPath, not Path: a directory called "dir with space" or "dir#hash"
	// must be redirected to a URL that still says so. The decoded form used to
	// emit "Location: /dir with space/", which a client cannot resolve, and
	// "Location: /dir#hash/", where the "#" swallows the token behind it.
	target := r.URL.EscapedPath() + "/"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusMovedPermanently)
}

// authenticated enforces the optional bearer token. With no token configured
// the server is open: it is a local/LAN tool and the operator opted in by not
// setting one. With a token every endpoint needs it, and files additionally
// accept ?access_token= so a URL copied out of the UI works in curl.
func (s *Server) authenticated(r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	presented := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		presented = strings.TrimPrefix(h, "Bearer ")
	} else if q := r.URL.Query().Get("access_token"); q != "" {
		presented = q
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.Token)) == 1
}

// setSecurityHeaders applies the §8 header set for pages the program authors:
// no sniffing, no referrer, no framing, and a CSP that only allows same-origin
// resources. Inline script and style are forbidden, which is why the UI's CSS and
// JS are separate embedded files rather than strings inside the HTML.
func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; media-src 'self'; frame-src 'self'; frame-ancestors 'none'")
}

// serveSecurityHeaders applies the §5.1 pair for served user files: sandbox
// plus nosniff, so a payload.html or an SVG is inert even if the content-type
// policy were ever wrong about it.
func serveSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Referrer-Policy", "no-referrer")
	// Served bytes are not the program's own output, so they carry no
	// freshness promise this server can keep: the file behind the URL can
	// change between two requests, and a cached copy would be a stale answer
	// to a request that is not conditional. ETag and Last-Modified already
	// give a revalidating client everything it needs, and no-store keeps a
	// token-protected response out of a shared cache.
	h.Set("Cache-Control", "no-store")
}

// IsLoopback reports whether addr binds only the local machine. It drives the
// §8 loud warning for a non-loopback bind running without a token.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ClampPreviewBounds keeps a configured preview cap inside a sane range.
func ClampPreviewBounds(n int64) int64 {
	const ceiling = 64 << 20
	switch {
	case n < 0:
		return 0
	case n > ceiling:
		return ceiling
	default:
		return n
	}
}

// version is stamped at build time via -ldflags; see the Makefile.
var version = "dev"

// Version reports the build version for --version.
func Version() string { return version }

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// headOnly drops the body of a HEAD response while keeping headers identical to
// the GET it stands for.
type headOnly struct{ http.ResponseWriter }

func (headOnly) Write([]byte) (int, error) { return 0, nil }

package fsx

import (
	"path"
	"strings"
)

// octetStream is the answer for everything that could execute in a browser.
// User content is never served as text/html, SVG or XML, so a payload.html in
// the served directory downloads as an opaque blob instead of running script in
// dirserve's origin.
const octetStream = "application/octet-stream"

// textExtensions are text in practice. They are served as
// "text/plain; charset=utf-8" with nosniff: inert, but readable — and that is
// exactly what the UI previews.
var textExtensions = map[string]bool{
	// docs & data
	"txt": true, "text": true, "log": true, "md": true, "markdown": true,
	"rst": true, "adoc": true, "csv": true, "tsv": true, "ini": true,
	"cfg": true, "conf": true, "env": true, "properties": true, "toml": true,
	"yaml": true, "yml": true, "json": true, "jsonc": true, "ndjson": true,
	"patch": true, "diff": true, "lock": true, "gitignore": true,
	// shell & scripts
	"sh": true, "bash": true, "zsh": true, "fish": true, "ksh": true,
	"ps1": true, "psm1": true, "bat": true, "cmd": true,
	"py": true, "rb": true, "pl": true, "pm": true, "lua": true, "r": true,
	"tcl": true, "awk": true, "sed": true,
	// web & systems
	"js": true, "mjs": true, "cjs": true, "ts": true, "tsx": true, "jsx": true,
	"css": true, "scss": true, "sass": true, "less": true, "go": true,
	"rs": true, "c": true, "h": true, "cc": true, "cpp": true, "cxx": true,
	"hpp": true, "hh": true, "java": true, "kt": true, "kts": true, "scala": true,
	"swift": true, "cs": true, "fs": true, "fsx": true, "vb": true, "dart": true,
	"zig": true, "nim": true, "cr": true, "ex": true, "exs": true, "erl": true,
	"hs": true, "ml": true, "clj": true, "groovy": true, "vue": true, "svelte": true,
	// infra
	"sql": true, "graphql": true, "gql": true, "proto": true, "tf": true,
	"tfvars": true, "hcl": true, "dockerfile": true, "makefile": true, "mk": true,
	"cmake": true, "gradle": true, "bzl": true, "nix": true, "service": true,
	"timer": true, "socket": true, "desktop": true, "plist": true, "gemspec": true,
	"crt": true, "pem": true, "pub": true, "sum": true, "tpl": true, "tpl.go": true,
	"tex": true, "bib": true, "org": true, "srt": true, "vtt": true, "ass": true,
}

// imageExtensions and mediaExtensions carry the honest type, because the UI
// previews them with <img>, <audio> and <video> and the browser needs the real
// value. None of them can script in the app origin: images cannot, and media
// rendering is contained by the sandbox header on every file response.
var imageExtensions = map[string]string{
	"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
	"gif": "image/gif", "webp": "image/webp", "avif": "image/avif",
	"bmp": "image/bmp", "ico": "image/x-icon", "apng": "image/apng",
	"tif": "image/tiff", "tiff": "image/tiff", "heic": "image/heic",
	"jxl": "image/jxl",
}

var mediaExtensions = map[string]string{
	"mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm",
	"ogv": "video/ogg", "mov": "video/quicktime", "mkv": "video/x-matroska",
	"avi": "video/x-msvideo", "mp3": "audio/mpeg", "m4a": "audio/mp4",
	"aac": "audio/aac", "wav": "audio/wav", "ogg": "audio/ogg", "oga": "audio/ogg",
	"opus": "audio/opus", "flac": "audio/flac", "weba": "audio/webm",
	"mid": "audio/midi", "midi": "audio/midi", "aiff": "audio/aiff",
}

// archiveExtensions are named so the UI can show a type badge; they still
// download as octet-stream-ish blobs via Content-Disposition.
var otherExtensions = map[string]string{
	"pdf": "application/pdf",
	"gz":  "application/gzip", "tgz": "application/gzip", "zip": "application/zip",
	"bz2": "application/x-bzip2", "xz": "application/x-xz",
	"zst": "application/zstd", "tar": "application/x-tar",
	"7z": "application/x-7z-compressed", "rar": "application/vnd.rar",
	"apk":  "application/vnd.android.package-archive",
	"wasm": "application/wasm", "iso": "application/x-iso9660-image",
	"deb": "application/vnd.debian.binary-package",
	"rpm": "application/x-rpm",
}

// inertSuffixes are never served under their own name regardless of case or of
// any compound extension ("payload.html", "report.HTM", "icon.svg", "feed.xml").
// A map keyed by extension cannot express "html" inside "page.html.old", so the
// check is a suffix pass over the lowercased name and runs before every table.
var inertSuffixes = []string{
	".html", ".htm", ".xhtml", ".xht", ".shtml", ".mhtml", ".hta", ".htc",
	".svg", ".svgz", ".xml", ".xsl", ".xslt", ".xbl", ".rdf", ".wsdl",
	".rss", ".atom", ".plist.html",
	".php", ".phtml", ".asp", ".aspx", ".jsp", ".jspx", ".cgi",
	".mustache", ".hbs", ".ejs", ".erb", ".jinja", ".j2", ".twig",
}

// extensionIsInert reports whether a name ends in something a browser would
// parse as a document type that can script.
func extensionIsInert(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range inertSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// ContentType maps a file name to the Content-Type served for it.
//
// Policy, in order: a name ending in an inert document suffix is always
// octet-stream; a known text, image, media, archive or pdf extension gets its
// honest type; any other known extension is still octet-stream; a name with no
// extension at all is sniffed from the file head, and only text resolves to
// text/plain. Unknown extensions are never sniffed — the extension is a strong
// enough signal that a binary .dat is binary.
func ContentType(name string, sniffText bool) string {
	if extensionIsInert(name) {
		return octetStream
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	switch {
	case textExtensions[ext]:
		return textPlain
	case imageExtensions[ext] != "":
		return imageExtensions[ext]
	case mediaExtensions[ext] != "":
		return mediaExtensions[ext]
	case otherExtensions[ext] != "":
		return otherExtensions[ext]
	case ext == "":
		// No extension: only the head of the file can tell us, and only text
		// is safe to name.
		if sniffText {
			return textPlain
		}
		return octetStream
	default:
		return octetStream
	}
}

// IsPreviewable reports whether the UI can render this content type inline.
func IsPreviewable(contentType string) bool {
	base, _, _ := strings.Cut(contentType, ";")
	switch base {
	case textPlain, "application/pdf",
		"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif",
		"image/bmp", "image/x-icon", "image/apng", "image/tiff",
		"image/heic", "image/jxl":
		return true
	}
	return strings.HasPrefix(base, "audio/") || strings.HasPrefix(base, "video/")
}

const textPlain = "text/plain; charset=utf-8"

// Package assets holds the UI files that are compiled into the binary.
//
// The files are a package rather than raw strings in Go source so they stay
// real CSS/JS/HTML: editable, lintable, and served with correct content types
// without a build step.
package assets

import _ "embed"

// IndexHTML is the single-page shell (§7).
//
//go:embed index.html
var IndexHTML []byte

// StyleCSS is the stylesheet. The CSP forbids inline styles, which is why this
// is a file rather than a <style> block.
//
//go:embed style.css
var StyleCSS []byte

// AppJS is the UI script. The CSP forbids inline scripts for the same reason.
//
//go:embed app.js
var AppJS []byte

// FaviconSVG is the site icon.
//
//go:embed favicon.svg
var FaviconSVG []byte

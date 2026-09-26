package fsx

import (
	"net/url"
	"strings"
)

// decodeOnce percent-decodes a raw URL path exactly once. url.PathUnescape
// decodes "%2f" to "/" too, which is what we want: a traversal attempt written
// as /..%2f..%2f is unescaped to "../../" and then rejected by the segment
// check, instead of travelling to the filesystem as one weird filename.
//
// Invalid escapes are an error rather than a silent passthrough, and the result
// must not contain NUL or other control characters: those are never legal in a
// URL path, and rejecting them here keeps them out of every downstream format.
func decodeOnce(urlPath string) (string, error) {
	decoded, err := url.PathUnescape(urlPath)
	if err != nil {
		return "", ErrUnsafePath
	}
	for _, r := range decoded {
		if r < 0x20 || r == 0x7f {
			return "", ErrUnsafePath
		}
	}
	return decoded, nil
}

// validSegment rejects the segment shapes that would make a URL path mean
// something other than what it literally says: "." and "..". Every other byte
// is allowed, so names with spaces, "#", "?" or non-UTF-8 bytes round-trip as
// themselves.
func validSegment(seg string) bool {
	return seg != "." && seg != ".."
}

// DecodePath turns a raw URL path into a root-relative path.
//
// The leading "/" names the root and is dropped before the path is split, so an
// empty element left in the list is a doubled separator ("a//b"), not the root.
// A single trailing "/" is the canonical spelling of a directory URL and is
// stripped. ".." is rejected outright rather than resolved — os.Root would
// refuse it anyway, but rejecting it here means no filesystem call is made at
// all before the answer is known.
//
// Nothing downstream re-parses the result: a name containing "%" or a lone "?"
// is a name, not an escape or a query.
func DecodePath(urlPath string) (string, error) {
	decoded, err := decodeOnce(urlPath)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(decoded, "/") {
		return "", ErrUnsafePath
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(decoded, "/"), "/")
	if trimmed == "" {
		return "", nil // "/" names the root
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || !validSegment(seg) {
			return "", ErrUnsafePath
		}
	}
	return trimmed, nil
}

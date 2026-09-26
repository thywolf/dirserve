package fsx

import (
	"net/url"
	"strings"
)

// decodeSegment percent-decodes one URL path segment exactly once, and refuses
// any decode that would change the segment's structure rather than its bytes.
//
// It exists because a whole path cannot be decoded in one pass: url.PathUnescape
// turns "%2f" into "/", so decoding "/a%2fb.txt" as a string produces the
// two-segment path "a/b.txt" — a *different file*, indistinguishable from the one
// the client asked for. A filename can never contain "/", so an encoded slash
// inside a segment has no valid reading: it is either a separator (which the
// client should have written) or an attempt to smuggle one past the segment
// check. Both are refused here.
//
// "%25" is a different matter and is honoured exactly once: the segment
// "100%25.txt" decodes to the name "100%.txt", and that name has exactly one
// URL spelling, "100%25.txt".
func decodeSegment(seg string) (string, error) {
	decoded, err := url.PathUnescape(seg)
	if err != nil {
		return "", ErrUnsafePath
	}
	if strings.Contains(decoded, "/") {
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

// DecodePath turns a raw, still-escaped URL path into a root-relative path.
//
// It takes the *escaped* form — net/http's r.URL.EscapedPath(), not
// r.URL.Path — because r.URL.Path has already been percent-decoded by the
// standard library. Decoding that a second time is what used to make a file
// named "a%2fb.txt" unreachable and collide with the directory "a/b.txt".
// EscapedPath hands back the bytes as they arrived, so the single decode that
// happens here is the only one that happens.
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
	if !strings.HasPrefix(urlPath, "/") {
		return "", ErrUnsafePath
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(urlPath, "/"), "/")
	if trimmed == "" {
		return "", nil // "/" names the root
	}
	raw := strings.Split(trimmed, "/")
	out := make([]string, 0, len(raw))
	for _, seg := range raw {
		// A doubled separator is a malformed path, not an empty name.
		if seg == "" {
			return "", ErrUnsafePath
		}
		// Check the escaped bytes as well, so "%2e%2e" is refused before it
		// is decoded into "..".
		if !validSegment(seg) {
			return "", ErrUnsafePath
		}
		decoded, err := decodeSegment(seg)
		if err != nil {
			return "", err
		}
		if !validSegment(decoded) {
			return "", ErrUnsafePath
		}
		out = append(out, decoded)
	}
	return strings.Join(out, "/"), nil
}

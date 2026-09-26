#!/bin/sh
# End-to-end acceptance for dirserve (§12.3).
#
# Builds if needed, serves a throwaway fixture tree, and asserts the HTTP
# contract with curl: the piping contract, the three-way directory negotiation,
# traversal and symlink confinement, Range, ?dl=1, and token mode.
# POSIX sh only; prints E2E PASS on success.

set -eu

BINARY=./dirserve
PORT="${DIRSERVE_E2E_PORT:-18080}"
TOKEN_PORT="${DIRSERVE_E2E_TOKEN_PORT:-18081}"
FIXTURE="$(mktemp -d)"
LOG="$(mktemp)"
SERVER_PID=""
FAILURES=0
BASE="http://127.0.0.1:${PORT}"
TOKEN_BASE="http://127.0.0.1:${TOKEN_PORT}"
# A file outside the served root, for the traversal and symlink checks.
SECRET="$(dirname "$FIXTURE")/dirserve-e2e-secret.txt"

cleanup() {
	[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
	rm -rf "$FIXTURE" "$LOG" "$SECRET"
}
trap cleanup EXIT INT TERM

say() { printf '  %s\n' "$*"; }

fail() {
	printf '  FAIL  %s\n' "$1"
	FAILURES=$((FAILURES + 1))
}

# check <label> <expected> <actual>
check() {
	if [ "$2" = "$3" ]; then
		say "ok    $1"
	else
		fail "$1"
		printf '        expected: %s\n        actual:   %s\n' "$2" "$3"
	fi
}

# check_contains <label> <needle> <haystack>
check_contains() {
	case "$3" in
	*"$2"*) say "ok    $1" ;;
	*)
		fail "$1"
		printf '        expected to contain: %s\n        actual: %s\n' "$2" "$3"
		;;
	esac
}

# check_not_contains <label> <needle> <haystack>
check_not_contains() {
	case "$3" in
	*"$2"*)
		fail "$1"
		printf '        expected NOT to contain: %s\n        actual: %s\n' "$2" "$3"
		;;
	*) say "ok    $1" ;;
	esac
}

# status <url> [curl args...] -> HTTP status code. Arguments go before the URL
# so that "-X POST" style flags are not mistaken for a second URL.
status() {
	url="$1"
	shift
	curl -s -o /dev/null -w '%{http_code}' "$@" "$url"
}

# ---------------------------------------------------------------- fixtures --

mkdir -p "$FIXTURE/scripts" "$FIXTURE/configs" "$FIXTURE/api" "$FIXTURE/nested/deep"

printf '#!/bin/sh\necho "setup ran"\n' >"$FIXTURE/scripts/setup.sh"
chmod +x "$FIXTURE/scripts/setup.sh"
printf 'port = 8080\n' >"$FIXTURE/configs/app.conf"
printf '{"token":"v"}\n' >"$FIXTURE/api/tokens.json"
printf 'ok\n' >"$FIXTURE/healthz"
printf 'line one\nline two\nline three\n' >"$FIXTURE/notes.txt"
printf '<script>alert(1)</script>\n' >"$FIXTURE/payload.html"
printf '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>\n' >"$FIXTURE/vector.svg"
printf 'hello from a space\n' >"$FIXTURE/my file #1.txt"
printf 'OUTSIDE-SECRET\n' >"$SECRET"

# Names that can only be spelled one way in a URL, which is the point: a name
# containing an encoded separator must not be reachable as two segments, and a
# name containing a literal percent must be reachable at all. z/f.txt and
# z%2ff.txt carry different bytes so serving the wrong one is visible.
mkdir -p "$FIXTURE/z" "$FIXTURE/dir%pct"
printf 'PLAIN-PATH\n' >"$FIXTURE/z/f.txt"
printf 'ENCODED-NAME\n' >"$FIXTURE/z%2ff.txt"
printf 'PCT-DIR\n' >"$FIXTURE/dir%pct/inner.txt"
printf 'PCT-NAME\n' >"$FIXTURE/100%.txt"

# 1 KiB of known bytes for the Range check.
i=0
: >"$FIXTURE/blob.bin"
while [ $i -lt 64 ]; do
	printf '0123456789abcdef' >>"$FIXTURE/blob.bin"
	i=$((i + 1))
done

# A symlink pointing out of the served root. ln(1) on a filesystem without symlink
# support may silently make a copy instead, which would turn a security check
# into a no-op — so confirm it really is a link before relying on it.
HAVE_SYMLINK=0
if ln -s "$SECRET" "$FIXTURE/escape.txt" 2>/dev/null; then
	if [ -L "$FIXTURE/escape.txt" ] || ls -ld "$FIXTURE/escape.txt" | grep -q '^l'; then
		HAVE_SYMLINK=1
	fi
fi
if [ "$HAVE_SYMLINK" -eq 0 ]; then
	rm -f "$FIXTURE/escape.txt"
	say "skip  symlink escape (no real symlinks on this filesystem)"
fi

# ------------------------------------------------------------------- build --

if [ ! -x "$BINARY" ]; then
	say "building $BINARY"
	CGO_ENABLED=0 go build -trimpath -o "$BINARY" ./cmd/dirserve
fi

# start_server <port> <expected status> [extra args...] — waits until the root
# answers with the expected code. The server's own output goes to a log file and
# never to this script's stdout: an inherited pipe would keep a `| tail` reader
# open long after the script exited.
start_server() {
	port="$1"
	expect="$2"
	shift 2
	"$BINARY" --root "$FIXTURE" --addr "127.0.0.1:${port}" --quiet "$@" >"$LOG" 2>&1 &
	SERVER_PID=$!
	tries=0
	while [ $tries -lt 100 ]; do
		if [ "$(status "http://127.0.0.1:${port}/")" = "$expect" ]; then
			return 0
		fi
		tries=$((tries + 1))
		sleep 0.1
	done
	printf 'server did not reach status %s on port %s\n' "$expect" "$port" >&2
	cat "$LOG" >&2 || true
	exit 1
}

stop_server() {
	[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
	wait "$SERVER_PID" 2>/dev/null || true
	SERVER_PID=""
}

printf 'dirserve e2e\n'
start_server "$PORT" 200

# ------------------------------------------------------- health + piping --

check "root answers 200 (the health check is the root)" "200" "$(status "$BASE/")"

# The bytes must arrive byte-for-byte, not merely be executable.
check "file GET returns the file's exact bytes" \
	"$(cat "$FIXTURE/scripts/setup.sh")" \
	"$(curl -fsSL "$BASE/scripts/setup.sh")"

check "curl | sh executes the served script" \
	"setup ran" \
	"$(curl -fsSL "$BASE/scripts/setup.sh" | sh)"

# Directories first, then files, each case-insensitive bytewise.
check "plain-text listing with no Accept header" \
	"api/
configs/
dir%pct/
nested/
scripts/
z/
100%.txt
blob.bin
healthz
my file #1.txt
notes.txt
payload.html
vector.svg
z%2ff.txt" \
	"$(curl -fsSL "$BASE/" | sed '/^$/d')"

check_contains "subdirectory listing is greppable" "setup.sh" \
	"$(curl -fsSL "$BASE/scripts/" | grep setup)"

check_contains "a name with a space and # round-trips" "my file #1.txt" \
	"$(curl -fsSL --path-as-is "$BASE/" | sed '/^$/d')"

# ------------------------------------------------------------ negotiation --

check "Accept: text/html returns the UI" \
	"text/html; charset=utf-8" \
	"$(curl -s -o /dev/null -w '%{content_type}' -H 'Accept: text/html' "$BASE/")"

check_contains "JSON listing via Accept: application/json" '"type":"dir"' \
	"$(curl -fsSL -H 'Accept: application/json' "$BASE/")"

check_contains "JSON entries carry the documented shape" '"mtime_unix"' \
	"$(curl -fsSL -H 'Accept: application/json' "$BASE/")"

check_contains "a directory named api browses like any other" "tokens.json" \
	"$(curl -fsSL -H 'Accept: application/json' "$BASE/api/")"

check "a file named healthz serves like any other" "ok" \
	"$(curl -fsSL "$BASE/healthz")"

# ------------------------------------------------------- content safety --

check "html is never served as text/html" "application/octet-stream" \
	"$(curl -s -o /dev/null -w '%{content_type}' "$BASE/payload.html")"

check "svg is never served as an image type" "application/octet-stream" \
	"$(curl -s -o /dev/null -w '%{content_type}' "$BASE/vector.svg")"

check "file responses carry nosniff" "nosniff" \
	"$(curl -sI "$BASE/notes.txt" | tr -d '\r' | awk 'tolower($1)=="x-content-type-options:"{print $2}')"

check "file responses carry a sandbox CSP" "sandbox" \
	"$(curl -sI "$BASE/notes.txt" | tr -d '\r' | awk 'tolower($1)=="content-security-policy:"{print $2}')"

# ---------------------------------------------------------------- escaping --

# --path-as-is stops curl squashing ".." client-side, so the server really is
# the thing rejecting these requests.
check_not_contains "encoded traversal is refused" "OUTSIDE-SECRET" \
	"$(curl -s --path-as-is "$BASE/..%2f..%2fetc%2fpasswd" || true)"

check "encoded traversal returns 4xx" "4" \
	"$(status --path-as-is "$BASE/..%2f..%2fetc%2fpasswd" | cut -c1)"

check "dot-dot segment returns 4xx" "4" \
	"$(status --path-as-is "$BASE/scripts/../../etc/passwd" | cut -c1)"

check "encoded newline returns 4xx" "4" \
	"$(status --path-as-is "$BASE/notes%0a.txt" | cut -c1)"

if [ "$HAVE_SYMLINK" -eq 1 ]; then
	check "symlink escape 404s" "404" "$(status "$BASE/escape.txt")"
	check_not_contains "symlink escape leaks nothing" "OUTSIDE-SECRET" \
		"$(curl -s "$BASE/escape.txt" || true)"
fi

# ------------------------------------------------- url spelling and names --

# One name, one URL. A path must never be decoded twice: the second decode
# would let "z%252ff.txt" (the only correct spelling of the file z%2ff.txt)
# collapse onto the two-segment path z/f.txt and serve a different file.
check "a name with an encoded separator serves its own bytes" "ENCODED-NAME" \
	"$(curl -s --path-as-is "$BASE/z%252ff.txt" | tr -d '\n')"

check "the plain two-segment path is still the plain path" "PLAIN-PATH" \
	"$(curl -s "$BASE/z/f.txt" | tr -d '\n')"

check "an encoded separator is not accepted as a separator" "400" \
	"$(status --path-as-is "$BASE/z%2ff.txt")"

check "a directory named with a percent is reachable" "200" \
	"$(status "$BASE/dir%25pct/")"

check "a file named with a percent is reachable" "PCT-NAME" \
	"$(curl -s "$BASE/100%25.txt" | tr -d '\n')"

# The redirect has to keep the URL escaped, or a client cannot follow it and a
# fragment in the name swallows whatever follows it in the query.
check "directory redirect stays escaped" "301" "$(status "$BASE/dir%25pct")"
check "redirect Location keeps percent-encoding" "/dir%25pct/" \
	"$(curl -sI "$BASE/dir%25pct" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')"

mkdir -p "$FIXTURE/my dir #1"
check "redirect for a name with a space stays escaped" "/my%20dir%20%231/" \
	"$(curl -sI "$BASE/my%20dir%20%231" | tr -d '\r' | awk 'tolower($1)=="location:"{print $2}')"

# --------------------------------------------------------------- headers --

check "UI page refuses framing" "1" \
	"$(curl -sD- -o /dev/null -H 'Accept: text/html' "$BASE/" | tr -d '\r' | \
		grep -i "^content-security-policy:" | grep -c "frame-ancestors 'none'")"

check "served files are not stored by caches" "no-store" \
	"$(curl -sI "$BASE/notes.txt" | tr -d '\r' | awk 'tolower($1)=="cache-control:"{print $2}')"

check "listings vary on Accept" "Accept" \
	"$(curl -sI -H 'Accept: application/json' "$BASE/" | tr -d '\r' | awk 'tolower($1)=="vary:"{print $2}')"

# ----------------------------------------------------------------- assets --

# A served file must win over the embedded copy, and it must stay inert: the
# UI's own script is never replaced by served content.
printf 'alert(1)\n' >"$FIXTURE/app.js"
check "a served file beats the embedded asset" "alert(1)" \
	"$(curl -s "$BASE/app.js" | tr -d '\n')"
check "the served asset is not served as script" "text/plain; charset=utf-8" \
	"$(curl -sI "$BASE/app.js" | tr -d '\r' | awk 'tolower($1)=="content-type:"{$1="";sub(/^ /,"");print}')"
rm -f "$FIXTURE/app.js"

# ------------------------------------------------------- range + download --

check "Range request returns 206" "206" \
	"$(curl -s -o /dev/null -w '%{http_code}' -H 'Range: bytes=0-15' "$BASE/blob.bin")"

check "Range request returns the right slice" "0123456789abcdef" \
	"$(curl -s -H 'Range: bytes=0-15' "$BASE/blob.bin")"

check "?dl=1 sends an attachment disposition" "attachment;" \
	"$(curl -sI "$BASE/notes.txt?dl=1" | tr -d '\r' | awk 'tolower($1)=="content-disposition:"{print $2}')"

# -------------------------------------------------------------- read-only --

check "POST is refused" "405" "$(status "$BASE/notes.txt" -X POST)"
check "DELETE is refused" "405" "$(status "$BASE/notes.txt" -X DELETE)"

stop_server

# ------------------------------------------------------------------ token --

start_server "$TOKEN_PORT" 401 --token s3cret

check "token mode rejects an unauthenticated root" "401" "$(status "$TOKEN_BASE/")"
check "token mode rejects an unauthenticated file" "401" "$(status "$TOKEN_BASE/notes.txt")"
check "token mode accepts the bearer header" "200" \
	"$(status "$TOKEN_BASE/notes.txt" -H 'Authorization: Bearer s3cret')"

check "token mode accepts ?access_token" "200" \
	"$(status "$TOKEN_BASE/notes.txt?access_token=s3cret")"

check "token mode rejects a wrong token" "401" \
	"$(status "$TOKEN_BASE/notes.txt" -H 'Authorization: Bearer wrong')"
stop_server

# ------------------------------------------------------------------- done --

if [ "$FAILURES" -gt 0 ]; then
	printf '\nE2E FAIL (%d check(s) failed)\n' "$FAILURES"
	exit 1
fi
printf '\nE2E PASS\n'

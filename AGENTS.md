# AGENTS.md

Working notes for anyone — human or model — changing this repository.

## What this is

`dirserve` serves one directory over HTTP, read-only. Two faces of the same
URL set: `curl -fsSL` gets you bytes, a browser gets a file browser. One
static binary, Go standard library only, no third-party modules.

## The two ideas that hold it together

Everything else follows from these. If a change contradicts them, the change is
wrong.

**1. One filesystem chokepoint.** Every `open`, `stat` and listing goes through
`internal/fsx`, which holds a single `*os.Root` opened on the served directory.
Path confinement is therefore a property of the package's shape, not something
each call site has to remember. Do not add a direct `os.Open`, `os.Stat`,
`os.ReadFile`, `filepath.Walk*` or `ioutil` call anywhere else in the tree —
including from `cmd/`. If `fsx` cannot express what you need, add a method to
`fsx`; do not reach around it.

**2. Negotiation instead of an API.** There are no reserved paths. No `/api`,
no `/healthz`, no prefix that means anything. Behaviour is a function of the
filesystem plus the `Accept` header plus one query parameter (`?dl=1`) — never
of the path. A directory named `api` is a directory; a file named `healthz` is
a file. Do not add a mux, a route table, or a special-case path. The web UI
reads the same directory URLs you do, with `Accept: application/json`.

## Layout

```
cmd/dirserve/main.go    flags, env, root resolution, graceful shutdown
internal/fsx/           ALL filesystem access: os.Root, decode-once path
                        validation, listing, binary sniffing, content-type policy
internal/server/        one handler: negotiate, then serve a file or a directory
assets/                 index.html, style.css, app.js (embedded, real files)
e2e.sh                  end-to-end acceptance: real server, real curl
.github/workflows/ci.yml
```

Roughly 975 lines of non-test Go and 1500 lines of assets.

## Commands

```sh
go build ./...                  # compile
go test ./...                   # 47 unit + integration tests
go vet ./... && gofmt -l .      # both must be clean
./e2e.sh                        # 29 assertions against a live server
go test -covermode=atomic -coverprofile=coverage.out ./...
```

`make` works but wants a POSIX shell; on Windows use `go` directly.

**`e2e.sh` is the real acceptance gate.** It builds if needed, starts a server
on a temp fixture tree, and asserts the HTTP contract with curl: the piping
contract, all three directory representations, traversal and symlink
confinement, `Range`, `?dl=1`, method rejection, and token mode. It is
POSIX `sh` — keep it that way, and keep it free of `bash`-isms.

## Rules that are easy to break by accident

- **`gofmt` clean and `go vet` clean.** CI fails on both. Run `gofmt -w .`.
- **No new dependencies.** `go.mod` has no `require` block, and CI has a test
  asserting that. If you need something, it has to be in the standard library.
- **The tree is flat, not nested.** In `app.js`, a directory's children are
  *siblings* after it, not elements inside it. A `.row` is a flex container, so
  nesting rows makes a subtree into one flex item and the layout collapses. This
  cost an afternoon; don't reintroduce it.
- **Everything from the filesystem reaches the DOM via `textContent`.** A
  filename is attacker-controlled. The only `innerHTML` calls in `app.js` write
  icon strings defined in that same file.
- **Never serve user content as `text/html`.** HTML/SVG/XML/templating files go
  out as `application/octet-stream`, and every file response carries `nosniff`
  plus `CSP: sandbox`. There is a test for this; read it before touching
  `fsx.ContentType`.
- **Errors are plain text, never HTML.** A curl client has to be able to read
  the reason, and a filename must never reach an HTML context.
- **Line endings.** `.gitattributes` forces LF for `*.sh` and the Dockerfile. A
  CRLF `e2e.sh` fails on Linux as `/bin/sh^M: bad interpreter`. Keep the file
  mode `100755` on `e2e.sh`; CI runs `./e2e.sh` directly.
- **Keep `internal/server/packaging_test.go` passing.** It guards the
  deployment contract: no `build:` key in the compose file, every `${VAR}` has
  a default, `/data` is mounted `:ro`, and the docker job has `needs: test`. If
  you change deployment, change that test deliberately, not by relaxing it.

## Testing conventions

- Table-driven for validation, content types and negotiation.
- Test the *contract*, not the implementation. A test that asserts a function
  was called, or that a string contains another string, catches nothing.
- The symlink tests **skip on Windows** (no privileges to create symlinks).
  They only really run on Linux. If you touch path confinement, verify on Linux
  or via `GOOS=linux go test -c` executed under WSL — a Windows-only green run
  proves nothing about symlink behaviour.
- `TestConfineSymlinkEscape` documents a genuine `os.Root` quirk: an *absolute*
  symlink is refused even when it points inside the root, while a *relative*
  in-root symlink resolves. That is intentional, and the test pins both halves.

## Deploy

`main` and `v*` tags publish `ghcr.io/thywolf/dirserve`. The `docker` job runs
only after the `test` job passes, so a broken build cannot publish a tag. After
a successful push the job POSTs to the `DEPLOY_WEBHOOK_URL` repo secret, which
triggers the redeploy. That step warns but does not fail on a non-2xx
response — the image is already published at that point, and failing would
misreport a good build as broken.

`docker-compose.yml` pulls the image; it never builds. Every setting is a
`${VAR:-default}` so the stack runs in Portainer without edits.

## Things that are deliberately not here

No auth beyond the optional bearer token, no TLS, no database, no caching
layer, no gzip, no file watching, no search index, no markdown rendering, no
syntax highlighting, no frontend framework, no build step for the assets, no
recursive directory sizing. If an idea feels like a feature of a bigger
product, it is out of scope — the size budget is the enforcement mechanism.

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
internal/fsx/           ALL filesystem access: os.Root, per-segment path
                        decoding, listing, binary sniffing, content-type policy
internal/server/        one handler: negotiate, then serve a file or a directory
assets/                 index.html, style.css, app.js (embedded, real files)
e2e.sh                  end-to-end acceptance: real server, real curl
.github/workflows/ci.yml
```

About 1400 lines of non-test Go and 2100 lines of assets.

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

It has no browser, so it says nothing about layout. A phone-sized viewport used
to squeeze the preview pane to 102px and let the document pan sideways, and the
whole HTTP contract stayed green. Layout changes need a real viewport check.

## Rules that are easy to break by accident

- **`gofmt` clean and `go vet` clean.** CI fails on both. Run `gofmt -w .`.
- **No new dependencies.** `go.mod` has no `require` block, and CI has a test
  asserting that. If you need something, it has to be in the standard library.
- **The tree is flat, not nested.** In `app.js`, a directory's children are
  *siblings* after it, not elements inside it. A `.row` is a flex container, so
  nesting rows makes a subtree into one flex item and the layout collapses. This
  cost an afternoon; don't reintroduce it.
- **The phone layout is a drawer, not a second column.** Below 760px `.body`
  collapses to one column and the tree becomes a fixed slide-over keyed off
  `documentElement.dataset.drawer`, which `setDrawer()` in `app.js` owns. CSS
  only reacts to that attribute; do not add a second source of truth. The
  trigger and the back chevron both live in `.topbar` *on purpose*: the scrim
  covers everything below it, so a control in `.head` cannot be tapped while
  the drawer is open. Putting one back down there needs a `z-index` patch and
  will be hit-tested as unreachable.
- **Grid columns here are `minmax(0, 1fr)`, never `auto` or bare `1fr`.** An
  auto column sizes to its widest row's min-content, so one long filename in
  the topbar or the pane widens the whole document and the page pans sideways
  on a phone. `.app` has the same trap with its *implicit* column, which is why
  it is declared explicitly. So does `.main`, whose auto column an unbreakable
  breadcrumb grows — declared for the same reason. The same reasoning puts
  `min-width: 0` on `.root span`: a flex item floors at its content width, so
  without it the served-path pill refuses to ellipsize and re-breaks the topbar.
- **The theme is one attribute and two palettes.** Light and dark are each
  defined once as prefixed tokens at the top of `style.css` and mapped onto the
  live names by the theme rules there: no attribute means "follow the OS", and
  `documentElement.dataset.theme` (owned by the theme block in `app.js`,
  persisted in localStorage) pins an explicit choice. Do not fork palette
  values into component rules, and do not add a third source of truth.
- **The asset-shadowing property is load-bearing.** A user file named
  `app.js` or `style.css` in the served tree shadows the embedded copy — that
  is the no-reserved-paths rule, and it is pinned by an e2e assertion. It is
  safe only because `fsx.ContentType` types `.js` and `.css` as `text/plain`
  and every file response carries `nosniff`, so a browser refuses to execute
  or apply the shadowed file. If the content-type policy ever starts serving
  `.js` as `text/javascript`, shadowing becomes attacker-controlled script in
  the UI's origin. Check with the e2e suite before "fixing" those types.
- **Everything from the filesystem reaches the DOM via `textContent`.** A
  filename is attacker-controlled. The only `innerHTML` calls in `app.js` write
  icon strings defined in that same file.
- **Never serve user content as `text/html`.** HTML/SVG/XML/templating files go
  out as `application/octet-stream`, and every file response carries `nosniff`
  plus `CSP: sandbox`. There is a test for this; read it before touching
  `fsx.ContentType`.
- **Errors are plain text, never HTML.** A curl client has to be able to read
  the reason, and a filename must never reach an HTML context.
- **Decode the escaped path, exactly once, per segment.** `fsx.DecodePath` takes
  `r.URL.EscapedPath()` — never `r.URL.Path`, which `net/http` has *already*
  decoded. Decoding it a second time is what used to make a file named
  `a%2fb.txt` unreachable and alias it onto the directory `a/b.txt`. And
  `redirectToSlash` must build its `Location` from `EscapedPath()` for the same
  reason: the decoded form emits a redirect no client can follow for any
  directory name with a space or a `#` in it. If you add a code path that turns a
  request into another URL, this is the rule it has to follow.
- **A name has exactly one URL spelling.** `%2f` inside a segment is refused, not
  decoded into a separator. Don't "fix" that into a decode-and-split; the whole
  point is that `a%2fb.txt` and `a/b.txt` must not be the same request.
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
- `FuzzDecodePath` pins the security invariants of the path parser. The seed
  corpus runs with the normal suite; the fuzzer runs on demand:
  `go test ./internal/fsx -run '^$' -fuzz FuzzDecodePath -fuzztime 60s`.
  Run it before committing changes to `path.go` or anything that feeds
  `os.Root`.
- The symlink tests **skip on Windows** (no privileges to create symlinks).
  They only really run on Linux. If you touch path confinement, verify under
  WSL — a Windows-only green run proves nothing about symlink behaviour. The
  recipe that works from Git Bash (no Go inside WSL; cross-compile, then run
  the Linux binaries there; `MSYS_NO_PATHCONV=1` stops Git Bash rewriting
  `/mnt/...` paths before wsl.exe sees them):

  ```sh
  GOOS=linux GOARCH=amd64 go test -c -o /tmp/wsl-fsx.test ./internal/fsx
  GOOS=linux GOARCH=amd64 go test -c -o /tmp/wsl-server.test ./internal/server
  wsl /mnt/c/<tmp>/wsl-fsx.test -test.run 'Symlink' -test.v

  # The full acceptance gate on Linux, including its symlink assertions:
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ./dirserve ./cmd/dirserve
  MSYS_NO_PATHCONV=1 wsl sh -c 'cd /mnt/c/<repo> && sh e2e.sh'
  rm ./dirserve
  ```

  The e2e fixture is created by `mktemp -d` inside WSL (native ext4), so its
  `ln -s` checks run for real there even though the repo sits on /mnt/c.
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

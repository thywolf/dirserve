# dirserve

A folder, on HTTP, read-only.

```sh
curl -fsSL http://127.0.0.1:8080/setup.sh | bash
```

Open the same URL in a browser and you get a file browser with previews. That's
the whole thing: one directory made reachable, and nothing added to it.

It works on a phone. Below 760px the tree becomes a slide-over drawer and the
preview takes the whole screen, so a link pasted into a phone browser is usable
without pinching to zoom. Light and dark follow the system, with a toggle in
the top bar that remembers the choice.

## Why

Sometimes a script lives on your laptop and needs to be somewhere a Docker
container or a phone can reach it. Or a build log, a `dist/` folder, some
configs, and you'd rather not wire up an S3 bucket for the afternoon.

Existing tools do this and are great at it — dufs, FileBrowser, nginx's
autoindex. This is the smallest thing that does the piping job properly, in one
binary you can `scp` somewhere and run.

## Run it

**Docker.** The image serves `/data`, so the volume is all you set:

```sh
docker run --rm -p 8080:8080 -v "$PWD/data:/data" ghcr.io/thywolf/dirserve:latest
```

The container runs as a non-root uid (65532 in the published image), so the
served directory must be readable by it — a root-owned `0700` directory makes
the server exit at startup with `open /data: permission denied`. `chmod o+rx`
on the directory fixes it.

**Portainer.** Paste `docker-compose.yml` into a stack. It pulls the published
image and every setting is a variable with a working default, so it runs
untouched — set only what you care about:

| Variable | Default | |
| --- | --- | --- |
| `DIRTO_SHARE` | `/srv/dirshare` | path on the Docker host to share |
| `TOKEN` | *(empty)* | bearer token — **set this** |
| `HOST_PORT` | `8080` | port on the host |
| `IMAGE` | `ghcr.io/thywolf/dirserve:latest` | |
| `HIDE_DOTFILES` | `false` | |
| `MAX_PREVIEW_BYTES` | `1048576` | UI preview cap |
| `RESTART_POLICY` | `unless-stopped` | |

```sh
DIRTO_SHARE=/srv/files TOKEN=$(openssl rand -hex 32) docker compose up -d
```

On Windows Docker Desktop, `DIRTO_SHARE` wants a drive path — `C:/Users/me/share`.

**A binary.** Each release publishes static binaries — no Go needed:

```sh
curl -fsSLO https://github.com/thywolf/dirserve/releases/latest/download/dirserve-linux-amd64
chmod +x dirserve-linux-amd64
./dirserve-linux-amd64 --root /srv/files
```

Binaries exist for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and
windows/amd64, each stamped with its version, with a SHA256SUMS file beside
them. To build instead: Go 1.25+ (for `os.Root`), nothing else:

```sh
go build -o dirserve ./cmd/dirserve
./dirserve --root /srv/files
```

Leave `--root` off and it serves the working directory. The startup line always
tells you which directory it picked:

```
dirserve serving /srv/files at http://127.0.0.1:8080
```

## Using it

Files are just files at their own URL. No prefix, no endpoint, no token in the
path:

```sh
curl -fsSL http://127.0.0.1:8080/configs/app.conf
curl -fsSL 'http://127.0.0.1:8080/notes.txt?dl=1'   # force a download
curl -fsS -H 'Range: bytes=0-1023' http://127.0.0.1:8080/video.mp4
```

Range requests, `If-Modified-Since` and `ETag` all work, so seeking in a video
and resuming a `curl` both behave. A 3 GB file streams; it never lands in
memory.

Directories are where it gets interesting, because the same URL gives you three
different things depending on `Accept`:

| | |
| --- | --- |
| `Accept: text/html` | the web UI |
| `Accept: application/json` | `{"path":…,"entries":[{"name","type","size","mtime_unix"}]}` |
| anything else | one name per line, `dir/` for directories |

That last one is why it's useful to scripts:

```sh
curl http://127.0.0.1:8080/ | grep setup.sh
curl -fsSL -H 'Accept: application/json' http://127.0.0.1:8080/ | jq .
```

Listings put directories first, then case-insensitive, capped at 5,000 entries.

### No reserved paths

There's no `/api`, no `/healthz`, no magic prefix. A directory called `api` is
a directory, and a file called `healthz` is a file. What you get depends on
`Accept` and `?dl=1` — never on the path.

Which also means the health check is just the root answering:

```sh
curl -fsS http://127.0.0.1:8080/ >/dev/null && echo up
```

## Flags

```
dirserve [--root DIR] [--addr HOST:PORT] [--hidden] [--token T]
         [--max-preview-bytes N] [--quiet] [--version]
```

| Flag | Env | Default | |
| --- | --- | --- | --- |
| `--root` | `DIRSERVE_ROOT` | see below | directory to serve |
| `--addr` | `DIRSERVE_ADDR` | `127.0.0.1:8080` | listen address |
| `--hidden` | `DIRSERVE_HIDDEN` | off | hide dotfiles, refuse to serve them |
| `--token` | `DIRSERVE_TOKEN` | none | require a bearer token |
| `--max-preview-bytes` | `DIRSERVE_MAX_PREVIEW_BYTES` | `1048576` | UI preview cap |
| `--quiet` | — | off | don't log requests |

Flags beat env, env beats defaults. No config file.

The root resolves in this order: `--root`, then `DIRSERVE_ROOT`, then `/data` if
it exists (which is what a Docker volume makes, so containers need no
configuration), then the working directory. A missing root exits immediately
rather than serving an empty directory and letting you wonder why.

## Security

Read-only isn't a setting here. Only `GET` and `HEAD` exist; every other verb
is `405`. The process never writes to the directory it serves.

Served files can't run anything in your browser. Every file response carries
`X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`, and
HTML, SVG, XML and templating files are served as `application/octet-stream` —
a `payload.html` in the directory downloads as an opaque blob. The UI's own
pages add `frame-ancestors 'none'`, so another site cannot iframe the browser.

Paths can't escape the root. All filesystem access goes through one `os.Root`.
`..`, percent-encoded traversal, NUL bytes and control characters are rejected
before any syscall, and symlinks pointing outside the root fail to resolve.

Each name has exactly one URL. A path is decoded once, per segment: `%2f`
inside a segment is refused rather than turned into a separator, so a file named
`a%2fb.txt` and a directory `a/b.txt` can never be the same request, and a
directory named `dir%pct` is reachable at `/dir%25pct/`.

With `--token`, every request needs `Authorization: Bearer <t>`. File URLs also
accept `?access_token=<t>` so a command you copy out of the UI works in curl.
The comparison is constant-time.

The default bind is loopback. Binding to anything wider without a token is
allowed — containers need it — but it prints a warning at startup, and it means
exactly what it says.

> Anyone who can reach the port can read the whole tree. That's the product.
> Put it on a LAN, a VPN, or behind something that terminates TLS, and give it a
> token if it's not just you.

## How it's built

1400 lines of Go, standard library only — `go.mod` has no `require` block and a
test enforces it. The UI is about 2100 lines of HTML/CSS/JS embedded in the
binary, served as real files so the CSP can forbid inline script and style.

Two decisions do most of the work:

**One filesystem chokepoint.** `internal/fsx` holds the only `*os.Root` in the
program. Every open, stat and listing goes through it, so confinement is
structural rather than a rule someone has to remember at each call site.

**Negotiation instead of an API.** The UI has no private endpoint to call — it
fetches the same directory URLs you do, with `Accept: application/json`. The
number of paths that mean something special is zero, which is why a directory
named `api` isn't a problem.

47 tests, plus `e2e.sh`: 29 assertions against a real server with real curl,
covering the piping contract, all three representations, traversal and symlink
confinement, `Range`, `?dl=1`, method rejection and token mode.

## CI

Every push to `main` and every `v*` tag runs `gofmt`, `go vet`, the tests with
a coverage floor, and `e2e.sh` on Go 1.25 and stable. The image is only built
if all of that passes, so a broken build can't publish a tag. After a
successful push, a webhook tells your deployment to pull the new image.
Version tags also cut a GitHub Release with static binaries for the five
common platforms and a SHA256SUMS file.

Pull requests build the image without pushing it, and additionally check that
it's under 10 MB, runs as non-root, and actually serves a mounted directory.

The published image is `FROM scratch`: 3.6 MB, no shell, no package manager,
no writable filesystem.

## Contributing

```sh
gofmt -w .
go test ./...
./e2e.sh
```

No new dependencies, and no new files reaching the filesystem outside
`internal/fsx`. `AGENTS.md` has the reasoning.

MIT.

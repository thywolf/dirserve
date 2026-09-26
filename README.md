# dirserve

Turn any directory into a readable HTTP endpoint. `curl -fsSL` a file from it
and the bytes arrive; open the same URL in a browser and you get a small, fast,
read-only file browser with previews. One static binary, no dependencies, no
database, no accounts — and no way to write, upload, edit or delete anything,
by construction rather than by policy.

Built for the `curl | bash` habit and for sharing a folder on a LAN, a home
server, or a container with one bind mount.

```
curl -fsSL http://127.0.0.1:8080/setup.sh | bash
curl -fsSL http://127.0.0.1:8080/configs/app.conf
```

## Quickstart

### Docker (no flags needed)

The published image serves `/data` automatically, so the volume is the only
thing to set:

```sh
docker run --rm -p 8080:8080 -v "$PWD/data:/data" ghcr.io/thywolf/dirserve:latest
```

### Docker Compose / Portainer

`docker-compose.yml` pulls the published image and is parameterized with
variables that all have working defaults, so it runs as-is and you only set
what you care about. In Portainer: **Stacks → Add stack → Web editor**, paste
the file, then define the variables under the stack's environment settings.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DIRTO_SHARE` | `/srv/dirshare` | absolute path on the Docker host to share |
| `TOKEN` | *(empty)* | bearer token; **set this in production** |
| `HOST_PORT` | `8080` | port on the Docker host |
| `IMAGE` | `ghcr.io/thywolf/dirserve:latest` | image to run |
| `HIDE_DOTFILES` | `false` | hide dotfiles from listings |
| `MAX_PREVIEW_BYTES` | `1048576` | UI inline preview cap |
| `RESTART_POLICY` | `unless-stopped` | compose restart policy |

```sh
DIRTO_SHARE=/srv/files TOKEN=$(openssl rand -hex 32) docker compose up -d
```

On Windows Docker Desktop, use a drive path for `DIRTO_SHARE`, e.g.

### Bare binary

```sh
go build -o dirserve ./cmd/dirserve   # or: make build
./dirserve                            # serves the current directory
./dirserve --root /srv/files --addr 127.0.0.1:9000
```

Go 1.25+ is required (`os.Root` is the confinement mechanism). There are no
third-party dependencies: `go.mod` has no `require` entries, and the UI is
embedded in the binary.

### Which directory gets served?

Resolved in this order, and printed in the startup line so the choice is always
visible:

1. `--root DIR`
2. `$DIRSERVE_ROOT`
3. `/data`, if it exists and is a directory (this is what a Docker volume
   creates — so containers need no configuration at all)
4. `.`

If the resolved root does not exist or is not a directory, dirserve exits
non-zero immediately rather than serving an empty surprise.

## URL rules

There are **no reserved paths**. No `/api`, no `/healthz`, no prefixes. A
directory named `api` is a directory like any other, and a file named `healthz`
downloads like any other. What you get depends only on `Accept` and one query
parameter.

### Files — the bytes, always

```sh
curl -fsSL http://127.0.0.1:8080/setup.sh
curl -fsSL http://127.0.0.1:8080/setup.sh?dl=1     # force a download
curl -fsS -H 'Range: bytes=0-1023' …               # 206, for seeking and resume
```

Files support `Range`, `If-Modified-Since` (304) and `ETag`, so video seeking
and resumable downloads work. A 3 GB file streams; it is never buffered whole.

### Directories — pick your representation with `Accept`

| Request | Response |
| --- | --- |
| `Accept: text/html` | the web UI (also the zero-JS plain listing) |
| `Accept: application/json` | `{"path":…,"entries":[{"name","type","size","mtime_unix"}]}` |
| anything else, or none | plain text, one name per line, `dir/` for directories |

```sh
curl http://127.0.0.1:8080/                       # a pipeable name list
curl -fsSL http://127.0.0.1:8080/ | grep setup
curl -fsSL -H 'Accept: application/json' http://127.0.0.1:8080/
```

Listings are sorted directories-first, then case-insensitive bytewise, and
capped at 5,000 entries per directory (`"truncated": true` beyond that).

### Health check

There is no health endpoint. The root answering `200` *is* the health check:

```sh
curl -fsS http://127.0.0.1:8080/ >/dev/null && echo up
```

## CLI and environment

```
dirserve [--root DIR] [--addr HOST:PORT] [--hidden] [--token T]
         [--max-preview-bytes N] [--quiet] [--version]
```

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `--root` | `DIRSERVE_ROOT` | `/data` if present, else `.` | directory to serve |
| `--addr` | `DIRSERVE_ADDR` | `127.0.0.1:8080` | listen address |
| `--hidden` | `DIRSERVE_HIDDEN` | off | hide dotfiles and refuse to serve them |
| `--token` | `DIRSERVE_TOKEN` | none | require `Authorization: Bearer <token>` |
| `--max-preview-bytes` | `DIRSERVE_MAX_PREVIEW_BYTES` | `1048576` | largest inline preview |
| `--quiet` | — | off | do not log requests |
| `--version` | — | — | print version and exit |

Flags beat environment variables, which beat defaults. There is no config file.
Startup prints one line:

```
dirserve serving /srv/files at http://127.0.0.1:8080
```

## Security

- **Read-only by construction.** Only `GET` and `HEAD` exist; everything else is
  `405`. The process never writes to the served directory.
- **No content can execute in the browser.** Every file response carries
  `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`, and
  HTML/HTM/SVG/XML/templating files are served as `application/octet-stream`. A
  `payload.html` in your directory downloads as an opaque blob.
- **Paths are confined.** All filesystem access goes through one `os.Root`
  chokepoint. `..`, percent-encoded traversal, NUL and control characters are
  rejected before any syscall, and symlinks that leave the root fail to resolve.
- **UI responses are locked down** with a same-origin CSP, `nosniff` and
  `Referrer-Policy: no-referrer`; there is no inline script or style.
- **Optional token.** With `--token`, every request needs
  `Authorization: Bearer <t>`, and file URLs also accept `?access_token=<t>` so
  a command copied out of the UI works in curl. Compared in constant time.
- **Bind policy.** The default bind is loopback. A non-loopback bind without a
  token is allowed (containers rely on it) but prints a loud warning at startup.

> **Do not expose this to the internet without a token.** It is designed for a
> laptop, a LAN, or behind your own reverse proxy. Anyone who can reach the port
> can read the entire tree, and that is the entire feature.

## Container image and CI

The image is published to GitHub Container Registry by
`.github/workflows/ci.yml`:

- On every push to `main` and on `v*` tags, CI runs `gofmt`, `go vet`, the unit
  and integration tests (with a coverage floor), and the `e2e.sh` acceptance
  script on Go 1.25 and the latest stable.
- Only if all of that passes does the `docker` job build and push the image.
  A failing test can therefore never produce a published `latest` tag.
- Pull requests build the image but do not push it, and additionally assert that
  the image is under 10 MB, runs as non-root, and actually serves a mounted
  directory.
- Tags pushed to `v1.2.3` become `ghcr.io/thywolf/dirserve:1.2.3` (and
  `sha-<short>`), with `latest` reserved for the default branch.

The image is `FROM scratch` with a single static binary, so it has no shell, no
package manager, and no writable filesystem.

## Contributing

```sh
gofmt -w .          # keep the tree gofmt-clean; CI enforces it
go test ./...       # unit and integration tests
./e2e.sh            # full HTTP contract acceptance run
```

## Design

```
dirserve
├── cmd/dirserve/main.go     flags, env, root resolution, graceful shutdown
├── internal/fsx/            the only filesystem access: os.Root, listing, sniffing
├── internal/server/         one handler: negotiate, then serve a file or a dir
└── assets/                  index.html, style.css, app.js (embedded)
```

Two ideas carry the design:

1. **One filesystem chokepoint.** Every open, stat and listing goes through
   `internal/fsx`, which holds the `os.Root`. Path safety is a property of the
   code's shape, not of remembering to check at each call site.
2. **Negotiation instead of an API.** There is no private endpoint for the UI to
   call. The UI fetches the same directory URLs you do, with
   `Accept: application/json`. The number of paths that mean something special
   is therefore zero, and a directory called `api` is not a special directory.

Read-only, tiny, and hard to misuse. MIT licensed.

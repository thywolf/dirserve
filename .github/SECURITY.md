# Security policy

## Reporting a vulnerability

Use GitHub's private vulnerability reporting (Security → Report a
vulnerability) rather than a public issue. Reports get a response within
a few days.

## Scope

dirserve serves one directory over HTTP, read-only. The threat model is
deliberately plain: **anyone who can reach the port can read the whole
tree.** That is the product, not a bug — put it on a LAN, a VPN, or
behind something that terminates TLS, and set a token if it is not just
you.

In scope: anything that lets a client read outside the served directory,
serve content that executes in the browser, bypass the token check, or
crash the server.

Out of scope: the read-only design itself, things the project does not
have by choice (no TLS, no auth beyond the token, no rate limiting), and
self-inflicted configuration such as serving `/`.

## Supported versions

Only the latest release is supported. It is a single static binary, so
upgrading is the whole patch.

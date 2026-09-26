# dirserve: build a static binary, ship it in a scratch image.
# No runtime network access, no shell, no package manager in the final image.

FROM golang:1.25-alpine AS build

WORKDIR /src

# go.mod first so the dependency layer caches; there are no dependencies, so this
# layer is a formality that keeps the build reproducible.
COPY go.mod ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY assets ./assets

# -trimpath keeps build paths out of the binary; ldflags set the version.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags "-s -w -X dirserve/internal/server.version=${VERSION}" \
	-o /out/dirserve ./cmd/dirserve

FROM scratch

# Root certificate bundle is not needed: dirserve never makes outbound requests.
# A non-root numeric UID is all a scratch image can have, which is exactly what
# "runs as non-root" needs here.
COPY --from=build /out/dirserve /dirserve
USER 65532:65532

EXPOSE 8080
VOLUME ["/data"]

# No --root here: the default resolution picks /data because the volume creates
# it, so `docker run -v ./data:/data dirserve` serves that directory with no
# flags. Remap elsewhere by passing --root explicitly.
ENTRYPOINT ["/dirserve"]
CMD ["--addr", "0.0.0.0:8080"]

# The API, the worker and the migrator, in one small image.
#
# # Why one image and not three
#
# They share a module, a config loader and a database layer, and they are always
# deployed together at the same version. Three images would be three copies of
# the same 30MB of dependencies and three things to keep in step; the entrypoint
# picks which binary runs. `docker compose` names them as three services, which
# is the part that actually matters operationally.
#
# # Why the result is small
#
# `scratch` — no distribution, no shell, no package manager. That is roughly
# 25MB total rather than the ~120MB a Debian slim base costs, and it is not only
# about disk: an image with no shell has no `sh` for an attacker who finds a
# command-injection bug to reach, and nothing to patch on a CVE Tuesday.
#
# The three things a static Go binary still needs from a distribution are copied
# in by hand:
#
#   * CA certificates, or every outbound TLS call fails — which for this
#     product means ZATCA submission, silently, at three in the morning.
#   * Zone data, because the accounting day is a shop's local day. Without it
#     `time.LoadLocation("Asia/Riyadh")` errors and a Z report lands on the
#     wrong date.
#   * /etc/passwd with a non-root user, so the container does not run as uid 0.

# --- build ------------------------------------------------------------------

FROM golang:1.26-alpine AS build

# git for the version stamp; tzdata so the zone database can be copied out.
RUN apk add --no-cache git tzdata ca-certificates

WORKDIR /src

# Dependencies first, in their own layer. They change on the order of once a
# month; the source changes many times a day, and a rebuild that re-downloads
# the module cache every time is a slow rebuild and a lot of wasted bandwidth.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# CGO_ENABLED=0 is what makes the binary static and `scratch` possible.
#
# -trimpath removes the build machine's directory layout from the binary, which
# is both smaller and one less thing leaked in a stack trace.
#
# -s -w drop the symbol table and DWARF. Roughly 25% off, and a panic still
# produces a usable stack — Go's traceback does not read either.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/migrate ./cmd/migrate

# A non-root user, built here because scratch has no adduser.
RUN echo 'rawsyst:x:10001:10001::/nonexistent:/sbin/nologin' > /out/passwd && \
    echo 'rawsyst:x:10001:' > /out/group

# --- run --------------------------------------------------------------------

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/passwd /etc/passwd
COPY --from=build /out/group /etc/group

COPY --from=build /out/api /api
COPY --from=build /out/worker /worker
COPY --from=build /out/migrate /migrate

# Never uid 0. A container that is rooted is a host that is one kernel bug away
# from being rooted.
USER 10001:10001

# The API's own default. The worker listens on nothing.
EXPOSE 8080

# No shell, so this is the exec form and there is no other option — which is the
# right form anyway: it makes the binary PID 1 and lets SIGTERM reach it, so the
# graceful shutdown in cmd/api actually runs.
ENTRYPOINT ["/api"]

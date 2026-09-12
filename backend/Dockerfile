# The API, the worker, the migrator, the bootstrap, the regulatory recorder
# and the source ingester, as one binary in one small image.
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
#
# One `go build`, not six. The six commands are one binary now (see
# cmd/biz1core), and the linker keeps one copy of everything they share.
ARG MODULE=github.com/mahedi-emon/Biz1core/backend
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X ${MODULE}/internal/build.Version=${VERSION}" \
      -o /out/biz1core ./cmd/biz1core

# A non-root user, built here because scratch has no adduser.
RUN echo 'biz1core:x:10001:10001::/nonexistent:/sbin/nologin' > /out/passwd && \
    echo 'biz1core:x:10001:' > /out/group

# --- run --------------------------------------------------------------------

# NAMED, and every compose service that wants this image asks for it by name.
#
# Docker builds the LAST stage in a file when no target is given, and the last
# stage here is `backup` — which is `postgres:17-alpine` and fourteen times the
# size. So a `docker build ./backend` with no `--target` quietly produces the
# heavy image and tags it `biz1core/backend`, and everything still WORKS, because
# compose overrides the entrypoint anyway. What is lost is the whole reason this
# stage is `scratch`: no shell for a command-injection bug to reach, and nothing
# to patch on a CVE Tuesday.
#
# Nothing catches that. The container runs, the API answers, and the only
# symptom is a number in `docker images` nobody reads. Hence the name, and hence
# `target: runtime` on every service below that uses it.
FROM scratch AS runtime

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/passwd /etc/passwd
COPY --from=build /out/group /etc/group

# One binary, seven commands: `biz1core api`, `biz1core worker`, `biz1core
# migrate`, `biz1core bootstrap`, `biz1core regulatory`, `biz1core ingest` and
# `biz1core backup`.
#
# It was six binaries, and they shared a module, a config loader, a database
# layer and a registry — so the image carried six copies of nearly the same
# thing: 22 + 12 + 11 + 10 + 10 + 11 MB against 21.5 MB for all of them
# together. `cmd/biz1core` says what each command is and why they still exist
# separately under `cmd/`.
COPY --from=build /out/biz1core /biz1core

# Never uid 0. A container that is rooted is a host that is one kernel bug away
# from being rooted.
USER 10001:10001

# The API's own default. The worker listens on nothing.
EXPOSE 8080

# No shell, so this is the exec form and there is no other option — which is the
# right form anyway: it makes the binary PID 1 and lets SIGTERM reach it, so the
# graceful shutdown in cmd/api actually runs.
ENTRYPOINT ["/biz1core", "api"]

# --- backup -----------------------------------------------------------------

# The same binary, on an image that has pg_dump.
#
# # Why a second image and not a tool in the first
#
# `pg_dump` and `pg_restore` are the right way to back up and restore a
# PostgreSQL database, and this product is not going to reimplement them. They
# cannot go in the runtime image above: it is `scratch`, which is the whole
# reason it is 33MB and has nothing for a command-injection bug to reach.
#
# So the backup runs on `postgres:17-alpine` — already pulled on any host that
# runs this stack, so it costs the 21MB of the binary and nothing else. It is
# also the version guarantee that actually matters: pg_dump refuses a server
# newer than itself, and taking the dumper from the same image as the database
# makes that impossible to get wrong.
#
# It is a separate TARGET rather than a separate Dockerfile so the binary is
# built once and both images take the same bytes.
FROM postgres:17-alpine AS backup

COPY --from=build /out/biz1core /biz1core

# Where a dump is staged, owned by the user that will write it.
#
# A named volume mounted here starts as a copy of this directory, ownership
# included — which is how a container that is not root gets a writable volume
# without a chown at start-up or a privileged entry point. Created in the image
# for exactly that reason.
RUN mkdir -p /staging && chown 70:70 /staging

# `postgres` (uid 70) ships with the image. Not root, and not the application's
# 10001 either: this container writes a dump to /staging and reads nothing of
# the application's.
USER 70:70

ENTRYPOINT ["/biz1core", "backup"]
CMD ["run"]

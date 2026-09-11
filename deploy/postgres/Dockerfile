# PostgreSQL, with the thing that ships its write-ahead log off the machine.
#
# # Why the database image is built here at all
#
# `archive_command` is a shell command PostgreSQL runs, inside the PostgreSQL
# container, once per segment. Whatever it names has to exist in that container.
# The options are a shell script that curls a presigned URL, a third-party tool
# added with `apk`, or this product's own binary — and the first two are worse
# for the same reason: the archiver has to encrypt with the same key and write
# the same sidecar as everything else that reads this archive, and a second
# implementation of that is a second thing to get wrong in the one place where
# being wrong is silent.
#
# So the same `/rawsyst` binary the backup agent runs is here too, and
# `archive_command` is `/rawsyst backup wal archive %p %f`. It is about twenty
# megabytes on top of an image that was already being pulled.
#
# # Why the build stage is repeated from backend/Dockerfile
#
# Because the two images have different build contexts. `backend/Dockerfile`
# builds from `./backend` and cannot see `deploy/postgres/`; this one builds
# from the repository root so it can copy both the source and the two shell
# scripts beside this file. The alternative — moving the deployment scripts
# under `backend/` so one Dockerfile could reach both — would put the database's
# configuration somewhere nobody would look for it.
#
# The flags below are the same flags. If they diverge, the binary in the
# database container stops being the binary everything else was tested with, so
# they are written out rather than abbreviated.

# --- build ------------------------------------------------------------------

FROM golang:1.26-alpine AS build

RUN apk add --no-cache git

WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY backend/ ./

ARG MODULE=github.com/mahedi-emon/rawsyst-pos/backend
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X ${MODULE}/internal/build.Version=${VERSION}" \
      -o /out/rawsyst ./cmd/rawsyst

# --- run --------------------------------------------------------------------

FROM postgres:17-alpine

# The archiver. Owned by root and not writable by the postgres user, which is
# the user that executes it: `archive_command` runs as the database user, and a
# binary that user could replace would be a way to turn a compromised database
# into arbitrary code with the object store's credentials.
COPY --from=build --chown=root:root --chmod=0755 /out/rawsyst /rawsyst

# One line of pg_hba.conf, added on every start, because pg_basebackup needs a
# `replication` entry that the image's own `host all all` line does not cover.
# See the script for the whole argument.
COPY --chown=root:root --chmod=0755 deploy/postgres/entrypoint.sh \
     /usr/local/bin/rawsyst-postgres-entrypoint.sh
COPY --chown=root:root --chmod=0755 deploy/postgres/initdb/ \
     /docker-entrypoint-initdb.d/

# The image's own entrypoint is still what starts PostgreSQL — the wrapper
# `exec`s it. Keeping `docker-entrypoint.sh` in the chain rather than replacing
# it is what preserves initdb, the POSTGRES_* variables, the ownership fixing
# and the `gosu` drop to the postgres user, all of which this product has no
# business reimplementing.
ENTRYPOINT ["rawsyst-postgres-entrypoint.sh"]
CMD ["postgres"]

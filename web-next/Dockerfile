# The back office.
#
# # Why this file exists, and what it replaces
#
# `docker-compose.yml` built `web/`, which is a two-page shell left over from
# the first front end. The product is `web-next`: 137 routes, every screen the
# Blueprint asks for. So a `docker compose up` deployed a sign-in and a portal
# stub and nothing else, and had done since the back office moved. Nothing
# caught it because both directories build, and a build that succeeds looks
# like a build that is right.
#
# # Why `output: 'standalone'` matters here
#
# Next's standalone build traces which files the server actually reaches and
# emits only those, plus a minimal `node_modules`. The alternative -- copying
# the whole workspace -- ships every dev dependency, every test file and the
# entire `@rawsyst/shared` source into a production image, which is roughly
# 400MB against about 180MB, and a lot of it is code that must never run in
# production.
#
# # Why the build stage needs the workspace and the runtime does not
#
# `@rawsyst/shared` is consumed FROM SOURCE -- deliberately, so the till and
# the browser cannot drift onto different versions of the same string
# catalogue. It therefore has to be present to compile, and must not be present
# afterwards.

# # Why slim (glibc) and not alpine (musl)
#
# Tailwind v4 compiles through a native Rust binary, shipped as one optional
# package per platform. npm selects it by platform and architecture and NOT by
# C library -- npm/cli#4828 -- so `npm ci` inside an alpine image installs
# `@tailwindcss/oxide-linux-x64-gnu` and the build then dies looking for
# `./tailwindcss-oxide.linux-x64-musl.node`. The lockfile carries both; npm
# picks the wrong one.
#
# Verified rather than assumed: `ls node_modules/@tailwindcss` in the alpine
# deps stage lists the gnu build on a musl image.
#
# Debian slim is what npm actually resolves for, so build and runtime agree on
# one libc. It costs about 30MB of base image against silently building the
# wrong thing.

# --- dependencies -----------------------------------------------------------

FROM node:22-slim AS deps
WORKDIR /repo

# The workspace manifests only. Installing before the source is copied means a
# source change does not invalidate the install layer, which is the difference
# between a ten-second rebuild and a two-minute one.
COPY package.json package-lock.json ./
COPY web-next/package.json web-next/
COPY web/package.json web/
COPY shared/package.json shared/
COPY pos/package.json pos/

# `npm ci` for the lockfile exactly. `npm install` would be free to resolve
# something newer, which is precisely what a production build must not do.
#
# --omit=dev is deliberately NOT set: Next needs its own toolchain and the
# TypeScript compiler to build, and both are devDependencies. The runtime stage
# below takes only what the standalone trace kept, so none of it ships.
RUN --mount=type=cache,target=/root/.npm \
    npm ci --workspaces --include-workspace-root

# --- build ------------------------------------------------------------------

FROM node:22-slim AS build
WORKDIR /repo

COPY --from=deps /repo/node_modules ./node_modules
COPY --from=deps /repo/web-next/node_modules ./web-next/node_modules
COPY package.json package-lock.json ./
COPY shared ./shared
COPY web-next ./web-next

# Telemetry off. A production build should not phone home, and a build that
# needs the network to finish is a build that fails in an air-gapped CI.
ENV NEXT_TELEMETRY_DISABLED=1
ENV NODE_ENV=production

# Where the back office proxies /api/v1 to.
#
# This has to be a BUILD argument, and finding that out cost a deployment that
# came up healthy and could not sign anybody in. Next resolves `rewrites()`
# when it builds and freezes the result into
# `.next/required-server-files.json`, so the runtime environment variable the
# compose file sets was read by nothing: every proxied request went to
# `http://localhost:8080`, inside the web container, where nothing listens.
#
# `http://api:8080` is the compose service, which is right for every deployment
# that uses this compose file. A deployment that puts the API somewhere else
# rebuilds with --build-arg. The default is deliberately NOT localhost: an
# image whose default only works on a developer's machine is an image that
# fails in the one place it is meant to run.
ARG RAWSYST_API_ORIGIN=http://api:8080
ENV RAWSYST_API_ORIGIN=$RAWSYST_API_ORIGIN

# A ceiling on the build, not a target.
#
# Next's compiler is happy to grow to whatever the machine has, and on a small
# build agent -- or a developer's 8GB laptop -- that means it competes with the
# database and the API for the last gigabyte and something gets killed by the
# OOM reaper. 1536MB builds this app with room to spare; measure before
# raising it.
ENV NODE_OPTIONS=--max-old-space-size=1536

WORKDIR /repo/web-next
RUN npm run build

# The image optimizer's dependencies, which nothing in this product calls.
#
# `next.config.mjs` sets `images: { unoptimized: true }`, and the reason is
# there: the back office renders icons as inline SVG and its one uploaded asset
# is served by an authenticated API route, which the optimizer cannot fetch.
# Next traces `sharp` into the standalone output anyway, because the code path
# exists in the server bundle whether or not a route reaches it — and `sharp`
# brings a libvips build per platform AND per C library, plus a WebAssembly
# fallback. Measured in the image: 44.3MB of a 92.7MB application layer.
#
# Two of those could go on any deployment: `linuxmusl` cannot load on a
# glibc runtime and `wasm32` is the fallback for when no native binary does.
# The rest goes because the feature is off, and `image-optimizer.test.ts`
# fails the build if anybody turns it back on without revisiting this.
RUN rm -rf .next/standalone/node_modules/@img \
           .next/standalone/node_modules/sharp

# --- run --------------------------------------------------------------------

# Distroless: a Node runtime, and nothing else. No shell, no package manager,
# no apt, nothing to patch on a CVE Tuesday and nothing for a command-injection
# bug to reach -- the same reasoning that puts the Go image on `scratch`.
#
# It is glibc, which matters: the standalone output carries sharp's native
# binary for both musl and gnu, and the gnu one is what a Debian-based runtime
# loads. Measured: node:22-slim gave a 451MB image for 93MB of application.
FROM gcr.io/distroless/nodejs22-debian12 AS run
WORKDIR /app

ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

# The server is a thin proxy in front of prerendered output; it does not need
# a large heap and should not take one when the box is under pressure.
ENV NODE_OPTIONS=--max-old-space-size=384

# `nonroot` (uid 65532) ships with the image, so there is no user to create.
# Ownership is set on the copy rather than with a later `chown -R`, which would
# duplicate every file into a second layer -- and there is no shell to run
# one with in any case.
COPY --from=build --chown=nonroot:nonroot /repo/web-next/.next/standalone ./
COPY --from=build --chown=nonroot:nonroot /repo/web-next/.next/static ./web-next/.next/static
COPY --from=build --chown=nonroot:nonroot /repo/web-next/public ./web-next/public

# Never uid 0. A container that is rooted is a host that is one kernel bug
# away from being rooted.
USER nonroot
EXPOSE 3000

# Answers without reaching the API on purpose. This says "the Node server is
# serving"; whether the API is up is the API's own healthcheck to answer, and
# conflating the two takes the back office out of rotation for a fault it does
# not have. There is no shell and no curl, so it is node asking itself, by
# absolute path -- a HEALTHCHECK does not go through ENTRYPOINT and there is
# no `node` on PATH here.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["/nodejs/bin/node", "-e", "fetch('http://127.0.0.1:3000/login').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]

# The image's ENTRYPOINT is already node, so this is the script alone rather
# than `node script`. Standalone emits its own server; `next start` is not
# involved and is not installed. The path mirrors the workspace layout inside
# the traced output.
CMD ["web-next/server.js"]

# The back office.
#
# # Why `output: 'standalone'` matters here
#
# Next's standalone build traces which files the server actually reaches and
# emits only those, plus a minimal `node_modules`. The alternative — copying the
# whole workspace — ships every dev dependency, every test file and the entire
# `@rawsyst/shared` source into a production image, which is roughly 400MB
# against about 180MB and a lot of it is code that must never run in production.
#
# # Why the build stage needs the workspace and the runtime does not
#
# `@rawsyst/shared` is consumed FROM SOURCE by both front ends — that is
# deliberate, so the till and the browser cannot drift onto different versions of
# the same component. It therefore has to be present to compile, and must not be
# present afterwards.

# --- dependencies -----------------------------------------------------------

FROM node:22-alpine AS deps
WORKDIR /repo

# The workspace manifests only. Installing before the source is copied means a
# source change does not invalidate the install layer, which is the difference
# between a ten-second rebuild and a two-minute one.
COPY package.json package-lock.json ./
COPY web/package.json web/
COPY shared/package.json shared/
COPY pos/package.json pos/

# `npm ci` for the lockfile exactly. `npm install` would be free to resolve
# something newer, which is precisely what a production build must not do.
RUN --mount=type=cache,target=/root/.npm \
    npm ci --workspaces --include-workspace-root

# --- build ------------------------------------------------------------------

FROM node:22-alpine AS build
WORKDIR /repo

COPY --from=deps /repo/node_modules ./node_modules
COPY --from=deps /repo/web/node_modules ./web/node_modules
COPY package.json package-lock.json ./
COPY shared ./shared
COPY web ./web

# Telemetry off. A production build should not phone home, and a build that
# needs the network to finish is a build that fails in an air-gapped CI.
ENV NEXT_TELEMETRY_DISABLED=1
ENV NODE_ENV=production

WORKDIR /repo/web
RUN npm run build

# --- run --------------------------------------------------------------------

FROM node:22-alpine AS run
WORKDIR /app

ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

# `node` (uid 1000) ships with the image, so there is no user to create.
# Ownership is set on the copy rather than with a later `chown -R`, which would
# duplicate every file into a second layer.
COPY --from=build --chown=node:node /repo/web/.next/standalone ./
COPY --from=build --chown=node:node /repo/web/.next/static ./web/.next/static
COPY --from=build --chown=node:node /repo/web/public ./web/public

USER node
EXPOSE 3000

# Standalone emits its own server; `next start` is not involved and is not
# installed. The path mirrors the workspace layout inside the traced output.
CMD ["node", "web/server.js"]

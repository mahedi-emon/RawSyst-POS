import { fileURLToPath } from 'node:url';

/**
 * The RawSyst web application.
 *
 * # Why the API is proxied rather than called cross-origin
 *
 * The Go service holds the durable half of a session in a `SameSite=Strict`
 * httpOnly cookie, and its CSRF partner has to be readable by `document.cookie`
 * on the page that echoes it. Both of those work only if the browser believes
 * the API is this site. Rewriting `/api/v1/*` here makes that true in
 * development and in production alike, and has the second benefit of removing
 * CORS from the picture entirely -- there is no origin list to keep in step
 * with a deployment.
 *
 * The Go service remains the security boundary. Nothing is re-implemented here;
 * this is a transport detail.
 */
const API_ORIGIN = process.env.RAWSYST_API_ORIGIN ?? 'http://localhost:8080';

/**
 * Hostnames the DEV server will serve its own chunks to.
 *
 * Development only. `next dev` refuses `/_next/*` to any origin it does not
 * recognise, answering 403 — which is a reasonable protection for a dev server
 * on a laptop, and is invisible until the day somebody tests the two-hostname
 * split locally. Then every chunk 403s, the page never hydrates, and the login
 * form silently falls back to a plain GET. Nothing in the console says
 * "hydration failed"; it just does not work.
 *
 * `next build` and `next start` have no such check, so this affects no
 * deployment. It is here so that testing the split locally is possible at all.
 *
 * Driven by the same variable the split itself reads, so there is one name to
 * set and no hostname written into the repository.
 */
const devOrigins = [
  process.env.RAWSYST_CONSOLE_HOST,
  process.env.RAWSYST_APP_HOST,
].filter(Boolean);

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,

  ...(devOrigins.length > 0 ? { allowedDevOrigins: devOrigins } : {}),

  // Deploys as a container beside the Go API rather than to a serverless host.
  output: 'standalone',

  // Nothing in this product uses `next/image`.
  //
  // The back office renders icons as inline SVG and the one uploaded asset —
  // a business's logo — is served by an authenticated API route, which is not
  // something the optimizer can fetch. So the image optimizer is dead weight,
  // and it is not cheap dead weight: it pulls `sharp` into the standalone
  // trace, and `sharp` ships a native binary per platform and libc plus a
  // WebAssembly fallback. Measured in the runtime image: 30MB of the 93MB
  // application layer, for a feature nothing calls.
  //
  // Saying so here removes it from the trace rather than deleting it
  // afterwards, so the build and the image agree about what is in the product.
  // The day somebody adds an <Image>, this line is what they will have to
  // change, and the comment above it says what it costs.
  images: { unoptimized: true },

  // Next writes AGENTS.md and CLAUDE.md into the project on first run. Refused:
  // files that instruct a coding agent are not build output, and a CLAUDE.md in
  // particular is read as instructions by any agent working in this repository
  // afterwards -- so a framework upgrade would quietly acquire the ability to
  // direct them.
  agentRules: false,

  // The string catalogue and the proven domain helpers are TypeScript source in
  // the workspace, so Next compiles them rather than expecting a published
  // build. Only non-UI modules are imported; the old front end's components are
  // deliberately not carried over.
  transpilePackages: ['@rawsyst/shared'],


  async rewrites() {
    return [
      { source: '/api/v1/:path*', destination: `${API_ORIGIN}/api/v1/:path*` },
    ];
  },

  turbopack: {
    resolveAlias: {
      '@rawsyst/shared': fileURLToPath(new URL('../shared/src', import.meta.url)),
    },
  },

  webpack(config) {
    config.resolve.alias['@rawsyst/shared'] = fileURLToPath(
      new URL('../shared/src', import.meta.url),
    );
    return config;
  },
};

export default nextConfig;

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

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,

  // Deploys as a container beside the Go API rather than to a serverless host.
  output: 'standalone',

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

import { fileURLToPath } from 'node:url';

/**
 * The back-office.
 *
 * It shares its screens with the Tauri POS rather than reimplementing them:
 * the dashboard, the drill-throughs and the buying module all live in
 * @rawsyst/shared and are consumed FROM SOURCE by both. One less build
 * artefact to keep in step, and — the actual reason — the two surfaces cannot
 * drift onto different versions of the same component, which is how a figure
 * ends up meaning one thing on a till and another on a laptop.
 *
 * `output: 'standalone'` because this deploys as a container beside the Go API
 * rather than to a serverless host.
 */
const nextConfig = {
  output: 'standalone',
  reactStrictMode: true,

  // Next 16 writes AGENTS.md and CLAUDE.md into this directory on first run.
  // Refused: files that instruct a coding agent are not build output. They
  // arrived untracked, would have gone in with the next `git add -A`, and a
  // CLAUDE.md in particular is read as instructions by any agent working in
  // this repository afterwards -- so a framework upgrade would quietly have
  // acquired the ability to direct them.
  agentRules: false,

  // The dev-mode indicator is a fixed overlay in the bottom-left corner,
  // which is exactly where this app puts the second row of its phone
  // navigation. It covered the Terminals link at 390px, so the link could not
  // be tapped while developing -- and the browser audit reported the section
  // as unopenable, which is the correct reading of what it saw.
  //
  // Only ever present in development, so nothing shipped was affected.
  devIndicators: false,

  // Shared is TypeScript source, so Next must compile it rather than expecting
  // a published build.
  transpilePackages: ['@rawsyst/shared'],

  webpack(config) {
    config.resolve.alias['@rawsyst/shared'] = fileURLToPath(
      new URL('../shared/src', import.meta.url),
    );
    return config;
  },
};

export default nextConfig;

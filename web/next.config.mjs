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

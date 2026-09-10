/**
 * The image optimizer is off, and the container image is built on that.
 *
 * Nothing in this product uses `next/image`: the back office renders icons as
 * inline SVG, and its one uploaded asset — a business's logo — is served by an
 * authenticated API route the optimizer could not fetch even if it were asked
 * to. So `next.config.mjs` sets `images: { unoptimized: true }`, and
 * `Dockerfile` deletes `sharp` and its libvips builds out of the standalone
 * output afterwards: 44.3MB of a 92.7MB application layer, for a feature with
 * no caller.
 *
 * That is a coupling between a config line and a `rm -rf` in a different file,
 * which is exactly the sort of thing that rots. This is the thread between
 * them. Turning the optimizer back on fails here, and the message says what
 * else has to change — rather than the deployment failing at runtime, on the
 * first request to an image, with a module-not-found for a package somebody
 * deleted eighteen months ago.
 */
import type { NextConfig } from 'next';
import { beforeAll, describe, expect, it } from 'vitest';

// Loaded dynamically because the config is JavaScript; its shape comes from
// `src/types/next-config.d.ts`, which says what that one module exports rather
// than turning `allowJs` on for the whole project.
let config: NextConfig;
beforeAll(async () => {
  config = (await import('../../next.config.mjs')).default;
});

describe('the image optimizer', () => {
  it('is off, because the Dockerfile deletes what it would need', () => {
    expect(
      config.images?.unoptimized,
      'next.config.mjs turns the image optimizer on, and the web Dockerfile ' +
        'removes sharp and @img from the standalone output because it was ' +
        'off. Take that `rm -rf` out before enabling this, or the container ' +
        'will fail on the first optimized image with a missing module.',
    ).toBe(true);
  });

  it('still deploys as a standalone server', () => {
    // The prune above edits `.next/standalone`. If the output mode ever
    // changes, that path stops existing and the build breaks in a way whose
    // cause is three files away.
    expect(config.output).toBe('standalone');
  });
});

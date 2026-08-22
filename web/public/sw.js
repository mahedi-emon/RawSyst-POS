// The service worker.
//
// Blueprint A7 asks for "offline caching on key screens". What it must not do
// is serve a figure that is no longer true: this is an application whose whole
// job is telling an owner what their cash position is, and a cached answer to
// that question is worse than no answer.
//
// So the rule here is narrow and deliberate:
//
//   /_next/static/*  cache-first.  Safe BY CONSTRUCTION — those filenames carry
//                    a content hash, so a given URL's bytes can never change.
//                    A new build produces new URLs.
//
//   the document     network-first, falling back to the last good copy. That
//                    gives the app shell offline; the data it then asks for
//                    still comes from the network or fails honestly.
//
//   everything else  network-only. Above all /api — no response from the API is
//                    ever cached, in either direction.
//
// The failure this avoids is the one that showed up during the responsive work:
// a stale HTML shell referencing asset URLs a newer build had replaced, which
// renders as an unstyled page rather than as an error. Network-first on the
// document means the shell is only ever stale when there is genuinely no
// network, and the hashed assets it names are the ones cached alongside it.

const SHELL = 'rawsyst-shell-v1';
const ASSETS = 'rawsyst-assets-v1';
const KEEP = new Set([SHELL, ASSETS]);

self.addEventListener('install', (event) => {
  // No precache list. The shell is whatever the first successful navigation
  // returns, which cannot drift out of step with the build the way a
  // hand-maintained list does.
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (!KEEP.has(name)) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener('fetch', (event) => {
  const { request } = event;

  // Only GET. A POST is a sale, a deposit or a shift close, and replaying one
  // from a cache would be a second transaction.
  if (request.method !== 'GET') return;

  const url = new URL(request.url);

  // Never our own origin's API, and never another origin.
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith('/api/')) return;

  if (url.pathname.startsWith('/_next/static/')) {
    event.respondWith(cacheFirst(request));
    return;
  }

  if (request.mode === 'navigate') {
    event.respondWith(networkFirst(request));
  }
});

async function cacheFirst(request) {
  const cache = await caches.open(ASSETS);
  const hit = await cache.match(request);
  if (hit) return hit;

  const response = await fetch(request);
  if (response.ok) cache.put(request, response.clone());
  return response;
}

async function networkFirst(request) {
  const cache = await caches.open(SHELL);
  try {
    const response = await fetch(request);
    if (response.ok) cache.put(request, response.clone());
    return response;
  } catch (offline) {
    const hit = await cache.match(request);
    if (hit) return hit;
    throw offline;
  }
}

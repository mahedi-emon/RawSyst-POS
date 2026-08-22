'use client';

// Registers the service worker, once, in the browser.
//
// Deliberately not in the Tauri till: that app has SQLite and a durable queue
// for offline, and a second offline mechanism layered underneath it would be
// two things to reason about when a sale goes missing. The worker is for the
// installable owner app (blueprint A7), which has no local database and needs
// its shell to survive a lift ride.

import { useEffect } from 'react';

export function ServiceWorker() {
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return;

    // After load rather than during it. Registration competes with the first
    // render for the network, and A7 treats mobile performance as a primary
    // target — the shell should paint before we start warming a cache.
    const register = () => {
      navigator.serviceWorker.register('/sw.js').catch(() => {
        // A failed registration costs offline support and nothing else. It
        // happens on an insecure origin, and in a private window in some
        // browsers, neither of which is worth an error in front of a user.
      });
    };

    if (document.readyState === 'complete') register();
    else {
      window.addEventListener('load', register, { once: true });
      return () => window.removeEventListener('load', register);
    }
  }, []);

  return null;
}

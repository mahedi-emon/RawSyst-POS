'use client';

// The one client boundary.
//
// AuthProvider holds a session and a live API client, so it has to run in the
// browser. Everything below it is shared with the Tauri POS and is a client
// component for the same reason: these are interactive operational screens, not
// documents, and there is no server-rendered version of "what is my cash
// position right now" that would still be true by the time it arrived.
//
// The API base comes from the environment because a deployment points at that
// tenant's region. NEXT_PUBLIC_ because the browser is what calls it — the Go
// service is the security boundary, not this app, and every route it exposes is
// authenticated and permission-gated server-side.

import { AuthProvider } from '@rawsyst/shared/auth/session';
import { LocaleProvider } from '@rawsyst/shared/i18n/locale';
import { CardTableLabels } from '@rawsyst/shared/ui/CardTableLabels';

import { BackOffice } from './BackOffice';
import { ServiceWorker } from './ServiceWorker';

// localhost, not 127.0.0.1, and the difference is not cosmetic.
//
// The refresh token is an httpOnly cookie now, and cookies are scoped by HOST.
// A dev server serving the page from localhost:3000 while calling an API at
// 127.0.0.1:8080 sets the cookie against a host the page is not on, so the
// browser never sends it back: sign-in appears to work and the session cannot
// be refreshed. Nothing errors, which is what makes it expensive to find.
//
// In a real deployment the API must be same-SITE with the app -- api.example.com
// beside app.example.com is fine, since SameSite is about the registrable
// domain -- or the Strict cookie will not travel and refresh will not work.
const baseUrl = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080';

export function Providers() {
  return (
    <LocaleProvider>
      <CardTableLabels />
      <ServiceWorker />
      <AuthProvider baseUrl={baseUrl}>
        <BackOffice />
      </AuthProvider>
    </LocaleProvider>
  );
}

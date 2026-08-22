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

const baseUrl = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://127.0.0.1:8080';

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

// The shell.
//
// Exactly one rule governs this file: nothing renders until a real session
// exists. There is no development bypass, no "skip login" flag and no default
// user — a till that could be opened without signing in would attribute every
// sale to nobody, and the cash session it belongs to could never be
// reconciled against a person.

import { useEffect, useState } from 'react';

import { LoginScreen } from './auth/LoginScreen';
import { useAuth } from './auth/session';
import { PosCounter } from './pos/PosCounter';
import { TerminalBanner } from './ui/TerminalBanner';
import { terminalCapabilities, type Capabilities } from './pos/terminal';

export function App() {
  const { status, me, signOut } = useAuth();
  const [caps, setCaps] = useState<Capabilities | null>(null);

  useEffect(() => {
    terminalCapabilities().then(setCaps).catch(() => setCaps(null));
  }, []);

  if (status === 'restoring') {
    return (
      <main className="splash" aria-busy="true">
        <p>Checking this terminal…</p>
      </main>
    );
  }

  if (status !== 'signed_in' || !me) {
    return <LoginScreen />;
  }

  // Selling is a permission like any other. A signed-in user without it — an
  // accountant, a stock controller — gets told plainly rather than shown a
  // till that would refuse every sale.
  const maySell = me.permissions.includes('sales.create');

  return (
    <div className="app">
      <header className="app__bar">
        <span className="app__brand">RawSyst POS</span>
        <div className="app__spacer" />
        <button className="button button--quiet" onClick={() => void signOut()}>
          Sign out
        </button>
      </header>

      {/* The terminal says plainly what it cannot do. A cashier must never be
          left assuming invoices are reaching ZATCA when signing is gated. */}
      <TerminalBanner caps={caps} />

      {maySell ? (
        <PosCounter />
      ) : (
        <main className="empty">
          <h2>This account cannot ring up sales</h2>
          <p>
            Your role does not include permission to sell. An owner can change
            that under Settings &gt; People.
          </p>
        </main>
      )}
    </div>
  );
}

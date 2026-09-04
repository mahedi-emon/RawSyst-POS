'use client';

// The last resort.
//
// A rendering error, not an API refusal -- those are handled by the screen that
// made the call, where there is enough context to say what failed and offer the
// right remedy. By the time something reaches here the screen is gone, so the
// only honest thing to offer is a retry and a way out.
//
// The error text itself is not shown. It is a stack trace or a framework
// message written for whoever wrote the code, and putting it in front of a
// shopkeeper tells them nothing while looking like the product has broken open.

import { useEffect } from 'react';

import { Button } from '@/components/ui/button';

export default function AppError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Kept in the console so a developer or a support engineer looking over
    // somebody's shoulder can see what happened.
    console.error('RawSyst screen failed to render', error);
  }, [error]);

  return (
    <main className="grid min-h-dvh place-items-center bg-ground px-4">
      <div className="max-w-md text-center">
        <h1 className="text-page font-semibold text-fg">
          This screen could not be shown
        </h1>
        <p className="mt-2 text-body text-muted">
          Nothing you were working on has been lost. Try loading it again — and
          if it keeps happening, tell support what you were doing.
        </p>
        <div className="mt-5 flex items-center justify-center gap-2">
          <Button variant="primary" onClick={reset}>
            Try again
          </Button>
          <Button asChild variant="secondary">
            <a href="/">Go to your workspace</a>
          </Button>
        </div>
        {error.digest && (
          <p className="mt-4 text-caption text-subtle">
            Reference {error.digest}
          </p>
        )}
      </div>
    </main>
  );
}

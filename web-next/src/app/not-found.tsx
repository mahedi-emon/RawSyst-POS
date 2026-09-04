import Link from 'next/link';

// A URL that is not a screen.
//
// Distinct from the 404 a record gets when it belongs to another business:
// that one is answered by the API and shown by `ErrorState`, and its wording is
// careful not to confirm whether the record exists. This one is simply an
// address that is not part of the product, and can say so plainly.

export default function NotFound() {
  return (
    <main className="grid min-h-dvh place-items-center bg-ground px-4">
      <div className="max-w-md text-center">
        <p className="text-label font-semibold text-muted">Not found</p>
        <h1 className="mt-1 text-page font-semibold text-fg">
          There is no page at this address
        </h1>
        <p className="mt-2 text-body text-muted">
          The link may be out of date, or it may have been typed slightly wrong.
        </p>
        <Link
          href="/"
          className="mt-5 inline-flex h-10 items-center rounded-sm border border-line-strong bg-surface px-4 text-body font-medium hover:bg-surface-hover"
        >
          Go to your workspace
        </Link>
      </div>
    </main>
  );
}

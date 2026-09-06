// Handing a fetched file to the browser.
//
// Separated from the screens because every one of them got it wrong in the
// same way first: a plain `<a href="/api/v1/...">` looks like a download and
// answers 401, since the API reads the bearer header and the access token
// lives in memory rather than in a cookie. The file has to be fetched with the
// token and then handed over, which is what this does.

/** Saves an already-fetched blob under a name, and releases the object URL. */
export function saveAs(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  // Appended before clicking: a detached anchor is ignored by Firefox, which
  // is the difference between this working everywhere and working in Chrome.
  document.body.append(link);
  link.click();
  link.remove();
  // Released on the next tick rather than immediately -- revoking while the
  // click is still being handled cancels the download in Safari.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

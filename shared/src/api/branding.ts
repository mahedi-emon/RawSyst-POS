// A client's own logo, blueprint I2.
//
// The mirror of api/devices.ts: the server owns the validation, the storage and
// what a logo is allowed to be, and this states what happened and reads the
// answer.
//
// # The bytes go as base64 in JSON
//
// Not multipart, because nothing else in this API is, and the one binary route
// that already existed — a terminal handing back a signed document — carries
// its bytes the same way. A second transport convention for one screen would be
// a second thing to keep right.
//
// # The content type is not sent
//
// Deliberately. The server decides what the file is by reading its header, so
// there is nothing here for a caller to lie with. A browser's reported MIME
// type is a guess from the file extension and is worth exactly that.

import type { Client } from './client';

export interface Logo {
  content_type: 'image/png' | 'image/jpeg';
  byte_size: number;
  width: number;
  height: number;
  /** SHA-256 of the bytes. Doubles as the cache validator on the image route,
   *  and as a cheap way for a screen to know the logo changed. */
  checksum: string;
  uploaded_at: string;
}

/** What is set, without the bytes. `null` means no logo — the ordinary state
 *  for a new business, and a panel rather than an error. */
export async function fetchLogo(
  client: Client,
  companyId: string,
): Promise<Logo | null> {
  const body = await client.send<{ logo: Logo | null }>(
    'GET',
    `/api/v1/companies/${companyId}/logo`,
  );
  return body.logo ?? null;
}

/** Where the file itself lives. Used as an `<img src>`, so the browser fetches
 *  it with the session cookie-less bearer flow the rest of the app uses — see
 *  `logoObjectURL` for why that needs care. */
export function logoImagePath(companyId: string): string {
  return `/api/v1/companies/${companyId}/logo/image`;
}

/**
 * Fetches the image as a blob URL for display.
 *
 * An `<img src>` pointing straight at the route would not carry the bearer
 * token — the browser does not attach Authorization to an image request — so
 * the preview is fetched through the client and turned into an object URL. The
 * caller must revoke it when the component unmounts, or the blob is held for
 * the life of the page.
 */
export async function logoObjectURL(
  client: Client,
  companyId: string,
): Promise<string> {
  const blob = await client.sendBlob('GET', logoImagePath(companyId));
  return URL.createObjectURL(blob);
}

/** Stores or replaces the logo. `data` is base64 of the raw file, with no
 *  `data:` prefix — see `readFileAsBase64`. */
export function putLogo(
  client: Client,
  companyId: string,
  data: string,
): Promise<Logo> {
  return client.send<Logo>('PUT', `/api/v1/companies/${companyId}/logo`, { data });
}

/** Removes the logo, returning the business to the default RawSyst mark.
 *  Removing one that is not there succeeds. */
export function deleteLogo(client: Client, companyId: string): Promise<void> {
  return client.send<void>('DELETE', `/api/v1/companies/${companyId}/logo`);
}

/**
 * Reads a picked file as base64.
 *
 * Via `arrayBuffer()` rather than `FileReader.readAsDataURL`, for two reasons.
 * The data URL would have to have its `data:image/png;base64,` prefix sliced
 * off again, which is work to undo work; and `FileReader` is a DOM API, so a
 * function built on it cannot be tested outside a browser. `Blob.arrayBuffer`
 * exists in both places and returns the bytes directly.
 *
 * The chunking matters. `String.fromCharCode(...bytes)` on a 512 KB file
 * spreads half a million arguments onto the stack and throws; 32 KB at a time
 * is well inside every engine's limit.
 */
export async function readFileAsBase64(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  const chunk = 0x8000;
  let binary = '';
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

// --- document templates (I2 / P35) ----------------------------------------

/** The document types a client may write their own stationery for.
 *
 *  I2 names nine. The other five are documents this product does not issue
 *  yet, and a template for one of those would be configuration for nothing. */
export type DocType = 'standard' | 'simplified' | 'credit_note' | 'debit_note';

/** What a client writes on one kind of document.
 *
 *  Presentation only. Nothing here reaches a figure, a party, a tax number or
 *  a date — those come off the document row, which is immutable. That is what
 *  makes a template safe to change after a document has been issued. */
export interface DocumentTemplate {
  doc_type: DocType;

  header_text: string;
  header_text_ar: string;
  footer_text: string;
  footer_text_ar: string;
  return_policy: string;
  return_policy_ar: string;
  payment_terms: string;
  payment_terms_ar: string;

  show_logo: boolean;
  show_tax_number: boolean;

  /** False for a type nobody has touched, so the screen shows it as the
   *  default rather than as an empty form somebody has to fill in. */
  configured: boolean;
  updated_at?: string;
}

export async function fetchTemplates(
  client: Client,
  companyId: string,
): Promise<DocumentTemplate[]> {
  const body = await client.send<{ data: DocumentTemplate[] }>(
    'GET',
    `/api/v1/companies/${companyId}/templates`,
  );
  return body.data ?? [];
}

export function saveTemplate(
  client: Client,
  companyId: string,
  template: DocumentTemplate,
): Promise<DocumentTemplate> {
  const { doc_type, configured, updated_at, ...body } = template;
  void configured;
  void updated_at;
  return client.send<DocumentTemplate>(
    'PUT',
    `/api/v1/companies/${companyId}/templates/${doc_type}`,
    body,
  );
}

/** Returns the type to the RawSyst default. Resetting one that was never
 *  customised succeeds: the client asked for the default and has it. */
export function resetTemplate(
  client: Client,
  companyId: string,
  docType: DocType,
): Promise<void> {
  return client.send<void>(
    'DELETE',
    `/api/v1/companies/${companyId}/templates/${docType}`,
  );
}

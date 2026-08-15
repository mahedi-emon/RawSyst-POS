// What the Tauri shell tells the interface about this terminal.
//
// Kept behind one module so the web layer never reaches for `invoke` directly.
// That matters less for tidiness than for review: every crossing into the shell
// is in this file, and the list of things the interface may ask for is short
// and readable.

import { invoke } from '@tauri-apps/api/core';

export interface KeyPresence {
  present: boolean;
  status:
    | 'not_started'
    | 'compliance_csid'
    | 'production_csid'
    | 'live'
    | 'revoked'
    | 'expired';
  serial: string | null;
  expires_at: string | null;
}

export interface Capabilities {
  /** False until the document format is verified against ZATCA's standard. */
  signing_available: boolean;
  key: KeyPresence;
}

/** Reads the terminal's capabilities.
 *
 * Falls back to "cannot sign" when the shell is unavailable — running under a
 * plain browser during development, for instance. Assuming the optimistic
 * answer would let a developer build a flow that only works where it is never
 * tested.
 */
export async function terminalCapabilities(): Promise<Capabilities> {
  try {
    return await invoke<Capabilities>('terminal_capabilities');
  } catch {
    return {
      signing_available: false,
      key: { present: false, status: 'not_started', serial: null, expires_at: null },
    };
  }
}

export interface SignedDocument {
  xml: string;
  stamp: string;
  qr_tlv: string;
}

export interface SigningUnavailable {
  reason: string;
  detail: string;
}

/** Signs an invoice on this terminal.
 *
 * Refuses until the format is verified. The caller must treat the refusal as a
 * normal outcome and NOT as a reason to abandon the sale: the sale is recorded
 * and queued either way, and only the reporting is outstanding.
 */
export async function signInvoice(
  payload: string,
): Promise<{ ok: true; doc: SignedDocument } | { ok: false; why: SigningUnavailable }> {
  try {
    const doc = await invoke<SignedDocument>('sign_invoice', { payload });
    return { ok: true, doc };
  } catch (e) {
    const why = e as SigningUnavailable;
    return {
      ok: false,
      why:
        why && typeof why === 'object' && 'detail' in why
          ? why
          : {
              reason: 'signing_unavailable',
              detail: 'This terminal cannot sign invoices at the moment.',
            },
    };
  }
}

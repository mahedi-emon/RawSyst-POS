import { describe, expect, it, vi } from 'vitest';

import { Offline, RequestFailed } from '../api/client';
import {
  deleteLogo,
  fetchLogo,
  logoImagePath,
  putLogo,
  readFileAsBase64,
  type Logo,
} from '../api/branding';

/** A client that records what it was asked and answers with what it is told. */
function stubClient(answers: Record<string, unknown> = {}) {
  const calls: Array<{ method: string; path: string; body?: unknown }> = [];
  return {
    calls,
    send: vi.fn(async (method: string, path: string, body?: unknown) => {
      calls.push({ method, path, body });
      const key = `${method} ${path}`;
      const answer = answers[key];
      if (answer instanceof Error) throw answer;
      return answer;
    }),
  } as never;
}

const logo: Logo = {
  content_type: 'image/png',
  byte_size: 12_345,
  width: 240,
  height: 120,
  checksum: 'a'.repeat(64),
  uploaded_at: '2026-08-21T10:00:00+03:00',
};

describe('reading what is set', () => {
  it('returns the logo the server reports', async () => {
    const client = stubClient({
      'GET /api/v1/companies/c1/logo': { logo },
    });
    expect(await fetchLogo(client, 'c1')).toEqual(logo);
  });

  it('treats an unset logo as null rather than an error', async () => {
    // The ordinary state for a new business. A screen that threw here would
    // show a failure panel to every client who has not uploaded one yet.
    const client = stubClient({ 'GET /api/v1/companies/c1/logo': { logo: null } });
    expect(await fetchLogo(client, 'c1')).toBeNull();
  });

  it('copes with a body that omits the field entirely', async () => {
    const client = stubClient({ 'GET /api/v1/companies/c1/logo': {} });
    expect(await fetchLogo(client, 'c1')).toBeNull();
  });

  it('scopes every call to the company it was given', async () => {
    // A group holds several businesses, each issuing invoices under its own
    // registration, so a call that forgot the company would brand the wrong one.
    const client = stubClient({
      'GET /api/v1/companies/alpha/logo': { logo: null },
      'GET /api/v1/companies/beta/logo': { logo: null },
    });
    await fetchLogo(client, 'alpha');
    await fetchLogo(client, 'beta');
    expect((client as never as { calls: Array<{ path: string }> }).calls.map((c) => c.path))
      .toEqual(['/api/v1/companies/alpha/logo', '/api/v1/companies/beta/logo']);
  });
});

describe('writing', () => {
  it('sends the base64 payload and nothing else', async () => {
    // No content type is sent, deliberately: the server reads the file's own
    // header, so there is nothing here for a caller to lie with.
    const client = stubClient({ 'PUT /api/v1/companies/c1/logo': logo });
    await putLogo(client, 'c1', 'QUJD');

    const call = (client as never as { calls: Array<{ method: string; body: unknown }> }).calls[0]!;
    expect(call.method).toBe('PUT');
    expect(call.body).toEqual({ data: 'QUJD' });
    expect(Object.keys(call.body as object)).toEqual(['data']);
  });

  it('removes with a DELETE on the company', async () => {
    const client = stubClient({ 'DELETE /api/v1/companies/c1/logo': undefined });
    await deleteLogo(client, 'c1');
    const call = (client as never as { calls: Array<{ method: string; path: string }> }).calls[0]!;
    expect(call.method).toBe('DELETE');
    expect(call.path).toBe('/api/v1/companies/c1/logo');
  });

  it("passes the server's refusal through rather than rewording it", async () => {
    // The server names the actual problem — the size, the format, the
    // dimensions — better than the client could guess at it.
    const refusal = new RequestFailed(400, {
      code: 'invalid_input',
      message: 'That image is 900 KB. A logo must be 512 KB or smaller.',
    });
    const client = stubClient({ 'PUT /api/v1/companies/c1/logo': refusal });

    await expect(putLogo(client, 'c1', 'QUJD')).rejects.toThrow(/900 KB/);
  });

  it('surfaces a dead connection as Offline, not as a refusal', async () => {
    // The two mean different things to a client: one will work later.
    const client = stubClient({ 'PUT /api/v1/companies/c1/logo': new Offline() });
    await expect(putLogo(client, 'c1', 'QUJD')).rejects.toBeInstanceOf(Offline);
  });
});

describe('the image route', () => {
  it('is under the company, so RLS can scope it', () => {
    expect(logoImagePath('c1')).toBe('/api/v1/companies/c1/logo/image');
  });
});

describe('reading a picked file', () => {
  it('sends base64 alone, with no data: prefix', async () => {
    // The server takes base64 and nothing else. A data URL would have to have
    // its prefix sliced off again, which is work to undo work.
    const base64 = await readFileAsBase64(new Blob([new Uint8Array([1, 2, 3])]));

    expect(base64).not.toContain('data:');
    expect(base64).not.toContain(',');
    // Three bytes encode to four base64 characters.
    expect(base64).toHaveLength(4);
  });

  it('round-trips the bytes exactly', async () => {
    const bytes = new Uint8Array([0x89, 0x50, 0x4e, 0x47]); // a PNG's magic
    const base64 = await readFileAsBase64(new Blob([bytes]));

    const decoded = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
    expect(Array.from(decoded)).toEqual(Array.from(bytes));
  });

  it('survives a file at the size limit', async () => {
    // String.fromCharCode(...bytes) on half a megabyte spreads 512k arguments
    // onto the stack and throws. The chunking is what stops that, and a logo
    // right at the cap is exactly where it would bite.
    const big = new Uint8Array(512 * 1024);
    for (let i = 0; i < big.length; i++) big[i] = i % 256;

    const base64 = await readFileAsBase64(new Blob([big]));
    const decoded = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));

    expect(decoded).toHaveLength(big.length);
    expect(decoded[0]).toBe(big[0]);
    expect(decoded[decoded.length - 1]).toBe(big[big.length - 1]);
  });

  it('handles every byte value, not just printable ones', async () => {
    // A PNG is binary; a conversion that went through a text decoding would
    // mangle anything above 0x7f and corrupt the image silently.
    const all = new Uint8Array(256);
    for (let i = 0; i < 256; i++) all[i] = i;

    const decoded = Uint8Array.from(
      atob(await readFileAsBase64(new Blob([all]))),
      (c) => c.charCodeAt(0),
    );
    expect(Array.from(decoded)).toEqual(Array.from(all));
  });
});

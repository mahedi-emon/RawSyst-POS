'use client';

// The shop's own mark, on its own documents.
//
// # Why this was missing and why that mattered
//
// The document template on this screen already had a "print the logo" switch,
// and there were four routes for the logo itself — read, upload, remove, and
// serve the file — with nothing calling any of them. So a shop could tick a box
// promising its logo on every receipt and had no way to supply one. The switch
// was honest about intent and impossible to satisfy.
//
// # The bytes go as base64, and the server decides what they are
//
// `PUT .../logo` takes base64 and the branding service reads the format out of
// the header rather than trusting a content type — PNG or JPEG, 512 KB, between
// 32 and 2048 pixels a side, and it says which rule was broken. The two checks
// here are the cheap ones a browser can make without a round trip; every
// refusal that matters is still the server's.
//
// # The preview is the served file, not the chosen one
//
// After a successful upload the image is re-read from
// `GET .../logo/image` with the checksum as a cache-buster, so what is shown is
// what a receipt will print rather than what the file picker had in memory. A
// preview drawn from the local file would look right after an upload the
// server had rejected.

import { useRef, useState } from 'react';

import { Button } from '@/components/ui/button';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

/** Mirrors branding.Logo. Null when the shop has not uploaded one. */
interface Logo {
  content_type: string;
  byte_size: number;
  width: number;
  height: number;
  checksum: string;
  uploaded_at: string;
}

/** The server's own limits, restated only to refuse before a round trip. */
const MAX_BYTES = 512 * 1024;

export function LogoPanel({ companyId }: { companyId: string }) {
  const t = useT();
  const input = useRef<HTMLInputElement>(null);

  const { data, isLoading, error, refetch } = useApi<Logo | null>(
    companyId ? `/companies/${companyId}/logo` : null,
    { company_id: companyId },
  );

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const logo = data && data.checksum ? data : null;

  async function upload(file: File) {
    setActionError(null);
    setNote(null);

    if (!/^image\/(png|jpeg)$/.test(file.type)) {
      setActionError(t('nx.biz.logoNotAnImage'));
      return;
    }
    if (file.size > MAX_BYTES) {
      setActionError(t('nx.biz.logoTooBig'));
      return;
    }

    setBusy(true);
    try {
      // FileReader gives `data:image/png;base64,AAA...`; the route takes the
      // base64 alone and refuses a stray prefix rather than storing it as part
      // of the image.
      const encoded = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onerror = () => reject(new Error('unreadable'));
        reader.onload = () => {
          const result = String(reader.result ?? '');
          resolve(result.slice(result.indexOf(',') + 1));
        };
        reader.readAsDataURL(file);
      });

      await api.put(`/companies/${companyId}/logo?company_id=${companyId}`, {
        data: encoded,
      });
      setNote(t('nx.biz.logoSaved'));
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
      if (input.current) input.current.value = '';
    }
  }

  async function remove() {
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.delete(`/companies/${companyId}/logo?company_id=${companyId}`);
      setNote(t('nx.biz.logoRemoved'));
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title={t('nx.biz.logoTitle')}
      description={t('nx.biz.logoHint')}
      className="mb-5"
    >
      {error ? <FormError message={messageFor(error, t)} /> : null}

      <div className="flex flex-wrap items-start gap-5">
        {logo ? (
          <>
            {/* A plain <img>: this is a byte stream behind an authenticated
                route, not an asset next/image can optimise, and the checksum
                is what makes the browser fetch the new one after a replace. */}
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={`/api/v1/companies/${companyId}/logo/image?company_id=${companyId}&v=${logo.checksum}`}
              alt={t('nx.biz.logoAlt')}
              className="max-h-24 max-w-48 rounded-sm border border-line bg-surface object-contain p-2"
            />
            <div className="flex flex-col gap-2">
              <p className="text-caption text-muted">
                {t('nx.biz.logoOn', {
                  width: String(logo.width),
                  height: String(logo.height),
                  size: String(Math.max(1, Math.round(logo.byte_size / 1024))),
                  date: logo.uploaded_at.slice(0, 10),
                })}
              </p>
              <div className="flex flex-wrap gap-2">
                <Button
                  disabled={busy}
                  onClick={() => input.current?.click()}
                >
                  {t('nx.biz.logoReplace')}
                </Button>
                <Button variant="destructive" disabled={busy} onClick={() => void remove()}>
                  {t('nx.biz.logoRemove')}
                </Button>
              </div>
            </div>
          </>
        ) : (
          <div className="flex flex-col gap-2">
            <p className="max-w-prose text-body text-muted">
              {isLoading ? '' : t('nx.biz.logoNone')}
            </p>
            <div>
              <Button disabled={busy} onClick={() => input.current?.click()}>
                {t('nx.biz.logoChoose')}
              </Button>
            </div>
          </div>
        )}
      </div>

      <input
        ref={input}
        type="file"
        accept="image/png,image/jpeg"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) void upload(file);
        }}
      />

      {actionError ? <FormError message={actionError} /> : null}
      {note ? <p className="mt-3 text-body text-muted">{note}</p> : null}
    </Panel>
  );
}

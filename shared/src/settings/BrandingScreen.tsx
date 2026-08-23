// A client's own logo, blueprint I2 / UI spec Part I.
//
// The point of this screen is the product requirement behind it: a client buys
// RawSyst and puts their own mark on it, and nobody edits source to do it for
// them.
//
// # Where it appears, stated exactly
//
// The logo now prints: UI spec §5's invoice detail carries it, and P35 added
// the words around it. It still does NOT reach the thermal receipt, which is 42
// columns of plain text by deliberate design so it prints on every counter
// printer, and text cannot hold an image. The panel says which is which — a
// settings screen that quietly overstates what it changed is worse than one
// that admits the gap.
//
// # The preview is a blob, not a src
//
// A browser does not attach an Authorization header to an `<img src>`, so
// pointing one at the image route would fetch it unauthenticated and get a 401.
// The bytes come through the API client and become an object URL, which is
// revoked on unmount.

import { useCallback, useEffect, useRef, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import {
  deleteLogo,
  fetchLogo,
  logoObjectURL,
  putLogo,
  readFileAsBase64,
  type Logo,
} from '../api/branding';
import { FormError } from '../ui/Form';
import { TemplatePanel } from './TemplatePanel';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

/** Mirrors the server's own limits so the copy can state them before a client
 *  picks a file. The server is still the authority — these numbers exist to
 *  save somebody the round trip, not to replace the check. */
const MAX_BYTES = 512 * 1024;
const ACCEPTED = 'image/png,image/jpeg';

type Load =
  | { state: 'loading' }
  | { state: 'ready'; logo: Logo | null }
  | { state: 'failed'; message: string; offline: boolean }
  | { state: 'denied' };

export function BrandingScreen({ companyId }: { companyId: string }) {
  const t = useT();
  const { client, can } = useAuth();
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [preview, setPreview] = useState<string | null>(null);
  const [busy, setBusy] = useState<'uploading' | 'removing' | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);

  // identity.edit is what the write routes require, and it is what company
  // setup already carries. Deliberately not a branding-specific verb: a new
  // permission would have to reach every tenant's cloned roles before anybody
  // could use the feature.
  const mayEdit = can('identity.edit');

  const reload = useCallback(async () => {
    setLoad({ state: 'loading' });
    try {
      setLoad({ state: 'ready', logo: await fetchLogo(client, companyId) });
    } catch (err) {
      if (err instanceof RequestFailed && err.status === 403) {
        setLoad({ state: 'denied' });
        return;
      }
      setLoad({
        state: 'failed',
        message: explain(err, t),
        offline: err instanceof Offline,
      });
    }
  }, [client, companyId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // The preview follows whatever is stored. Keyed on the checksum so replacing
  // a logo fetches the new bytes rather than showing the old ones from a URL
  // that never changed.
  const checksum = load.state === 'ready' ? (load.logo?.checksum ?? null) : null;
  useEffect(() => {
    if (!checksum) {
      setPreview(null);
      return;
    }
    let url: string | null = null;
    let cancelled = false;
    logoObjectURL(client, companyId)
      .then((made) => {
        url = made;
        if (cancelled) URL.revokeObjectURL(made);
        else setPreview(made);
      })
      .catch(() => {
        if (!cancelled) setPreview(null);
      });
    return () => {
      cancelled = true;
      // Revoked, or the blob is held for the life of the page — and this panel
      // is reachable many times in a session.
      if (url) URL.revokeObjectURL(url);
    };
  }, [client, companyId, checksum]);

  async function upload(file: File) {
    setProblem(null);
    setSaved(null);

    // Two checks before the round trip, both of which the server repeats. Size
    // because uploading half a megabyte to be told it was too big is a poor
    // way to learn, and type because the file picker's accept attribute is a
    // suggestion a determined browser ignores.
    if (file.size > MAX_BYTES) {
      setProblem(
        `That image is ${Math.round(file.size / 1024)} KB. A logo must be ` +
          `${MAX_BYTES / 1024} KB or smaller — it is reprinted on every receipt.`,
      );
      return;
    }
    if (file.type && file.type !== 'image/png' && file.type !== 'image/jpeg') {
      setProblem(t('brand.formatHint'));
      return;
    }

    setBusy('uploading');
    try {
      const data = await readFileAsBase64(file);
      const logo = await putLogo(client, companyId, data);
      setLoad({ state: 'ready', logo });
      setSaved(t('brand.saved'));
    } catch (err) {
      setProblem(explain(err, t));
    } finally {
      setBusy(null);
      // Cleared so picking the SAME file again still fires a change event,
      // which is what a client does after a failed upload.
      if (picker.current) picker.current.value = '';
    }
  }

  async function remove() {
    setProblem(null);
    setSaved(null);
    setBusy('removing');
    try {
      await deleteLogo(client, companyId);
      setLoad({ state: 'ready', logo: null });
      setSaved(t('brand.removed'));
    } catch (err) {
      setProblem(explain(err, t));
    } finally {
      setBusy(null);
    }
  }

  if (load.state === 'loading') {
    return (
      <Shell>
        <div className="ds-skeleton" style={{ blockSize: 180 }} />
      </Shell>
    );
  }

  if (load.state === 'denied') {
    return (
      <Shell>
        <div className="ds-state">
          <p className="ds-state__title">{t('brand.noBrandingAccess')}</p>
          <p className="ds-state__body">
            Branding sits with the rest of your business settings. An owner can
            change what your role reaches under Settings &gt; People.
          </p>
        </div>
      </Shell>
    );
  }

  if (load.state === 'failed') {
    return (
      <Shell>
        <div className="ds-state">
          <p className="ds-state__title">
            {load.offline ? t('brand.unreachable') : t('brand.unreadable')}
          </p>
          <p className="ds-state__body">{load.message}</p>
          <button className="ds-btn ds-btn--secondary" onClick={() => void reload()}>
            {t('common.tryAgain')}
          </button>
        </div>
      </Shell>
    );
  }

  const { logo } = load;

  return (
    <main className="brand">
      <div className="ds-panel brand__panel">
        <div className="ds-panel__head">
          <h1 className="ds-h1">{t('brand.branding')}</h1>
          {logo && <span className="ds-badge ds-badge--success">{t('brand.logoSet')}</span>}
        </div>

        <div className="ds-panel__body">
          <p className="ds-body-sm ds-muted brand__lede">{t('brand.belongsToBusiness')}</p>

          <div className="brand__frame" aria-live="polite">
            {logo ? (
              preview ? (
                // Sized by the frame rather than by the file, so a wide mark
                // and a square one both sit correctly.
                <img className="brand__logo" src={preview} alt={t('brand.yourLogo')} />
              ) : (
                <div className="ds-skeleton" style={{ blockSize: 120, inlineSize: 200 }} />
              )
            ) : (
              <div className="brand__empty">
                <p className="ds-state__title">{t('brand.noLogo')}</p>
                <p className="ds-state__body">
                  {t('brand.defaultMark')}
                </p>
              </div>
            )}
          </div>

          {logo && (
            <dl className="brand__facts">
              <div>
                <dt className="ds-caption">{t('brand.format')}</dt>
                <dd>{logo.content_type === 'image/png' ? 'PNG' : 'JPEG'}</dd>
              </div>
              <div>
                <dt className="ds-caption">{t('brand.size')}</dt>
                <dd className="num">{Math.max(1, Math.round(logo.byte_size / 1024))} KB</dd>
              </div>
              <div>
                <dt className="ds-caption">{t('brand.dimensions')}</dt>
                <dd className="num">
                  {logo.width} &times; {logo.height}
                </dd>
              </div>
            </dl>
          )}

          {problem && <FormError message={problem} />}
          {saved && (
            <p className="brand__saved ds-body-sm" role="status">
              {saved}
            </p>
          )}

          {mayEdit ? (
            <>
              <input
                ref={picker}
                type="file"
                accept={ACCEPTED}
                className="ds-visually-hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) void upload(file);
                }}
              />
              <div className="brand__actions">
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy !== null}
                  onClick={() => picker.current?.click()}
                >
                  {busy === 'uploading'
                    ? t('brand.uploading')
                    : logo
                      ? t('brand.replaceLogo')
                      : t('brand.uploadLogo')}
                </button>
                {logo && (
                  <button
                    className="ds-btn ds-btn--danger"
                    disabled={busy !== null}
                    onClick={() => void remove()}
                  >
                    {busy === 'removing' ? t('brand.removing') : 'Remove'}
                  </button>
                )}
              </div>
              <p className="ds-caption brand__rules">
                PNG or JPEG, up to {MAX_BYTES / 1024} KB, between 32 and 2048
                pixels on each side. SVG is not accepted — it is a document
                rather than an image, and one uploaded here could carry code.
              </p>
            </>
          ) : (
            <p className="brand__readonly ds-body-sm" role="note">{t('brand.readOnly')}</p>
          )}

          {/* The honest note. Nothing prints this yet, and a settings screen
              that implied otherwise would have a client wondering why their
              receipts had not changed. */}
          <p className="brand__scope ds-body-sm" role="note">
            <strong>{t('brand.whereAppears')}</strong>{t('brand.whereItAppears')}</p>
        </div>
      </div>

      {/* The words, under the picture. Same screen because it is the same
          question a client is answering: what do my documents look like. */}
      <TemplatePanel companyId={companyId} />
    </main>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="brand">
      <div className="ds-panel brand__panel">
        <div className="ds-panel__body">{children}</div>
      </div>
    </main>
  );
}

/** Turns a failure into something an owner can act on. */
function explain(err: unknown, t: (key: Key) => string): string {
  if (err instanceof Offline) {
    return (
      t('brand.offlineOnServer') +
      'connection is back. Nothing already saved is lost.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return t('brand.notAllowed');
    if (err.status === 404) return t('brand.businessNotFound');
    // 400 carries the server's own sentence, which names the actual problem —
    // the size, the format, the dimensions — better than anything here could.
    return err.message;
  }
  return err instanceof Error ? err.message : t('common.somethingWrong');
}

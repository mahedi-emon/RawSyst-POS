'use client';

// "Is this still covered?" — F2's warranty check.
//
// # It answers only for a serial the shop sold to THIS caller
//
// The route is explicit about it, and the reason matters enough to repeat on
// the screen: answering for any serial would let anybody type numbers until
// they learned what a shop had sold and to whom. So "not found" here means
// "not one of yours", and the screen says that rather than "no such product",
// which would be a different and untrue statement.
//
// # In warranty is a fact, not a colour
//
// `in_warranty` is a boolean the server computed against the expiry date. The
// screen shows the word and the date, because a customer standing at a counter
// is about to repeat this to somebody.

import { ShieldCheck } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { Skeleton } from '@/components/ui/states';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { portalApi } from '@/lib/portal/client';
import { usePortalGuard } from '@/lib/portal/hooks';
import { usePortalSession } from '@/lib/portal/session';
import type { Warranty } from '@/lib/portal/types';

export default function PortalWarrantyPage() {
  const t = useT();
  const waiting = usePortalGuard('/portal');
  const { shop, token } = usePortalSession();

  const [serial, setSerial] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [answer, setAnswer] = useState<Warranty | null>(null);
  const [notYours, setNotYours] = useState(false);

  async function check() {
    if (!shop) return;
    setBusy(true);
    setError(null);
    setAnswer(null);
    setNotYours(false);
    try {
      const out = await portalApi.get<{ warranty: Warranty }>(
        '/portal/warranty',
        { shop, token, query: { serial_no: serial.trim() } },
      );
      setAnswer(out.warranty);
    } catch (e) {
      // A serial the shop did not sell this caller is a not-found, and the
      // sentence for it is about whose it is rather than whether it exists.
      if (e instanceof ApiError && e.status === 404) setNotYours(true);
      else setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (waiting) return <Skeleton className="h-48" />;

  return (
    <div className="flex flex-col gap-6">
      <Panel title={t('nx.sp.warrantyTitle')} description={t('nx.sp.warrantyLead')}>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void check();
          }}
        >
          <FormError message={error} />
          <Field
            name="serial_no"
            label={t('nx.sp.fSerial')}
            hint={t('nx.sp.fSerialHint')}
            required
          >
            <Input
              dir="ltr"
              className="num"
              value={serial}
              onChange={(e) => setSerial(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              autoFocus
            />
          </Field>
          <Button
            type="submit"
            variant="primary"
            busy={busy}
            disabled={serial.trim() === ''}
          >
            {t('nx.sp.check')}
          </Button>
        </form>
      </Panel>

      {notYours ? (
        <Panel title={t('nx.sp.notYoursTitle')}>
          <p className="text-body text-muted">{t('nx.sp.notYoursBody')}</p>
        </Panel>
      ) : null}

      {answer ? (
        <Panel title={answer.product ?? answer.serial_no}>
          <div className="flex flex-col gap-4">
            <p className="flex items-center gap-2">
              <ShieldCheck aria-hidden className="size-5" />
              <Badge tone={answer.in_warranty ? 'positive' : 'neutral'}>
                {answer.in_warranty
                  ? t('nx.sp.inWarranty')
                  : t('nx.sp.outOfWarranty')}
              </Badge>
            </p>
            <dl className="grid gap-3 text-body sm:grid-cols-2">
              <div>
                <dt className="text-caption text-muted">{t('nx.sp.fSerial')}</dt>
                <dd className="num" dir="ltr">
                  {answer.serial_no}
                </dd>
              </div>
              <div>
                <dt className="text-caption text-muted">{t('nx.sp.status')}</dt>
                <dd>{answer.status}</dd>
              </div>
              {answer.sold_on ? (
                <div>
                  <dt className="text-caption text-muted">{t('nx.sp.soldOn')}</dt>
                  <dd className="num">{answer.sold_on}</dd>
                </div>
              ) : null}
              {answer.expires_on ? (
                <div>
                  <dt className="text-caption text-muted">{t('nx.sp.coverUntil')}</dt>
                  <dd className="num">{answer.expires_on}</dd>
                </div>
              ) : null}
            </dl>
          </div>
        </Panel>
      ) : null}
    </div>
  );
}

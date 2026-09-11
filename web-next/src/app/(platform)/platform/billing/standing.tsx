'use client';

// Stopping a business, and starting it again.
//
// # Why this did not exist until now
//
// Phase 3 built the rest of this screen and deliberately left these three
// buttons out, because `tenant.status` was written by dunning and read by
// nothing. A button labelled "suspend" that suspended nobody would have been
// worse than no button: an operator would have pressed it, believed a client
// had been stopped, and gone home.
//
// The enforcement exists now — `subscribed()` in the api package refuses every
// operational write from a business that is not in good standing — so the
// controls can exist too.
//
// # Why suspend and deactivate are not the same button with a dropdown
//
// One of them is reversible and the other is how a client leaves. Putting them
// behind the same control, distinguished by a select somebody has to read,
// is how the wrong one gets pressed.

import { AlertTriangle, Ban, Pause, Play } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';
import type { Standing } from '@/lib/subscription-state';
import { toneFor } from '@/lib/subscription-state';

export function StandingPanel({ tenantId }: { tenantId: string }) {
  const t = useT();
  const { data, refetch } = useApi<{ standing: Standing }>(
    tenantId ? `/platform/tenants/${tenantId}/standing` : null,
    undefined,
    // Uncached and re-read after every press: this is the screen with the
    // buttons on it, and being behind the one just pressed is how an operator
    // presses it twice.
    { staleTime: 0 },
  );

  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const standing = data?.standing;
  if (!standing) return null;

  async function act(action: 'suspend' | 'activate' | 'deactivate') {
    setBusy(action);
    setError(null);
    setNote(null);
    try {
      await api.put(`/platform/tenants/${tenantId}/standing`, {
        action,
        reason,
      });
      setReason('');
      setNote(t(`nx.plat.st${action}ed` as 'nx.plat.stsuspended'));
      void refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  const tone = toneFor(standing.state);
  const stopped = !standing.write_allowed;

  return (
    <Panel
      title={t('nx.plat.stTitle')}
      description={t('nx.plat.stDesc')}
      className="mb-4"
    >
      <FormError message={error} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-3 border-b border-line pb-4">
        <span className="text-label text-muted">{t('nx.plat.stNow')}</span>
        {tone ? (
          <Badge tone={tone}>{standing.state.replace(/_/g, ' ')}</Badge>
        ) : (
          <span className="text-body text-fg">{standing.state}</span>
        )}
        {/* What the state actually stops, said rather than left to be
            inferred from a coloured word. */}
        <span className="text-caption text-subtle">
          {stopped ? t('nx.plat.stReadOnly') : t('nx.plat.stTrading')}
        </span>
      </div>

      {/* Expired is not a button. It is the calendar, and the remedy is to
          change the date on the plan above rather than to press anything
          here — so the panel says so instead of offering a control that
          would not fix it. */}
      {standing.state === 'expired' ? (
        <p className="mt-4 flex items-start gap-2 text-body text-muted">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
          {t('nx.plat.stExpiredHint')}
        </p>
      ) : null}

      <div className="mt-4 max-w-xl">
        <Field
          name="reason"
          label={t('nx.plat.stReason')}
          hint={t('nx.plat.stReasonHint')}
        >
          <Input value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </div>

      <div className="mt-4 flex flex-wrap gap-2">
        {standing.tenant_status === 'active' ? (
          <Button
            variant="secondary"
            busy={busy === 'suspend'}
            busyLabel={t('nx.plat.stWorking')}
            onClick={() => void act('suspend')}
          >
            <Pause className="size-4" aria-hidden />
            {t('nx.plat.stSuspend')}
          </Button>
        ) : (
          <Button
            busy={busy === 'activate'}
            busyLabel={t('nx.plat.stWorking')}
            onClick={() => void act('activate')}
          >
            <Play className="size-4" aria-hidden />
            {t('nx.plat.stActivate')}
          </Button>
        )}

        {standing.tenant_status !== 'deactivated' ? (
          <Button
            variant="destructive"
            busy={busy === 'deactivate'}
            busyLabel={t('nx.plat.stWorking')}
            onClick={() => void act('deactivate')}
          >
            <Ban className="size-4" aria-hidden />
            {t('nx.plat.stDeactivate')}
          </Button>
        ) : null}
      </div>

      <p className="mt-3 max-w-prose text-caption text-subtle">
        {t('nx.plat.stFootnote')}
      </p>
    </Panel>
  );
}

'use client';

// One counter's own configuration.
//
// # Why this could not be reached
//
// `GET` and `PUT /devices/{id}/settings` were both live, and
// `lib/devices/hardware.ts` already carried `settingsChange` with its own
// tests — the helper that works out which fields a screen actually changed.
// The screen it was written for was never built, so a shop could register a
// till and never tell it which printer to use, which warehouse it sells out
// of, or whether it may take a sale without naming a customer.
//
// # Only what changed is sent
//
// Every field on the route is a pointer and an absent one is left alone,
// "precisely so a screen changing the printer should not have to restate the
// discount rule". Sending the whole record back would undo that: two people
// editing different settings on one till would each overwrite the other with
// whatever they happened to have loaded. `settingsChange` is what keeps that
// true, and it is why the empty-string cases below are deliberate rather than
// sloppy — clearing a printer name IS a change, and it is sent as one.

import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { settingsChange, type TerminalSettings } from '@/lib/devices/hardware';
import { useT } from '@/lib/i18n/locale';

/** A place stock is held. The till sells out of exactly one of them. */
interface Warehouse {
  id: string;
  code: string;
  name: string;
  kind: string;
}

export function TerminalSettingsPanel({
  companyId,
  deviceId,
  label,
  mayManage,
  onClose,
}: {
  companyId: string;
  deviceId: string;
  label: string;
  mayManage: boolean;
  onClose: () => void;
}) {
  const t = useT();

  const settings = useApi<TerminalSettings>(`/devices/${deviceId}/settings`, {
    company_id: companyId,
  });
  const warehouses = useApiList<Warehouse>('/stock/locations', {
    company_id: companyId,
  });

  const [draft, setDraft] = useState<TerminalSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    if (settings.data) setDraft(settings.data);
  }, [settings.data]);

  async function save() {
    if (!draft || !settings.data) return;
    const change = settingsChange(settings.data, draft);
    if (Object.keys(change).length === 0) {
      setNote(t('nx.dev.settingsUnchanged'));
      return;
    }
    setBusy(true);
    setError(null);
    setFields({});
    try {
      await api.put(`/devices/${deviceId}/settings?company_id=${companyId}`, change);
      setNote(t('nx.dev.settingsSaved', { till: label }));
      await settings.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      className="mb-5"
      title={t('nx.dev.settingsTitle', { till: label })}
      description={t('nx.dev.settingsHint')}
      actions={
        <Button variant="ghost" onClick={onClose}>
          {t('nx.dev.settingsClose')}
        </Button>
      }
    >
      <FormError message={error} fields={fields} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {settings.error ? (
        <ErrorState error={settings.error} onRetry={() => void settings.refetch()} />
      ) : null}
      {settings.isLoading && !draft ? <Skeleton className="h-40" /> : null}

      {draft ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field
              name="default_warehouse_id"
              label={t('nx.dev.sellsFrom')}
              hint={t('nx.dev.sellsFromHint')}
              error={fields.default_warehouse_id}
            >
              <Select
                value={draft.default_warehouse_id ?? ''}
                disabled={!mayManage}
                onChange={(e) =>
                  setDraft({ ...draft, default_warehouse_id: e.target.value })
                }
              >
                <option value="">{t('nx.dev.branchDefault')}</option>
                {(warehouses.data?.data ?? []).map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </Select>
            </Field>

            <Field
              name="printer_name"
              label={t('nx.dev.printer')}
              hint={t('nx.dev.printerHint')}
            >
              <Input
                value={draft.printer_name ?? ''}
                disabled={!mayManage}
                onChange={(e) => setDraft({ ...draft, printer_name: e.target.value })}
              />
            </Field>

            <Field
              name="receipt_template"
              label={t('nx.dev.receiptTemplate')}
              hint={t('nx.dev.receiptTemplateHint')}
            >
              <Input
                value={draft.receipt_template ?? ''}
                disabled={!mayManage}
                onChange={(e) => setDraft({ ...draft, receipt_template: e.target.value })}
              />
            </Field>

            <Field
              name="scanner_prefix"
              label={t('nx.dev.scannerPrefix')}
              hint={t('nx.dev.scannerPrefixHint')}
            >
              <Input
                dir="ltr"
                className="num"
                value={draft.scanner_prefix ?? ''}
                disabled={!mayManage}
                onChange={(e) => setDraft({ ...draft, scanner_prefix: e.target.value })}
              />
            </Field>

            <Field
              name="max_held_carts"
              label={t('nx.dev.heldCarts')}
              hint={t('nx.dev.heldCartsHint')}
              error={fields.max_held_carts}
            >
              <Input
                numeric
                inputMode="numeric"
                value={draft.max_held_carts === undefined ? '' : String(draft.max_held_carts)}
                disabled={!mayManage}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    max_held_carts: e.target.value === '' ? undefined : Number(e.target.value),
                  })
                }
              />
            </Field>

            <Field
              name="default_discount_rule"
              label={t('nx.dev.discountRule')}
              hint={t('nx.dev.discountRuleHint')}
            >
              <Input
                value={draft.default_discount_rule ?? ''}
                disabled={!mayManage}
                onChange={(e) =>
                  setDraft({ ...draft, default_discount_rule: e.target.value })
                }
              />
            </Field>
          </div>

          <div className="mt-4 flex flex-col gap-3">
            <Checkbox
              label={t('nx.dev.drawerEnabled')}
              hint={t('nx.dev.drawerEnabledHint')}
              checked={Boolean(draft.drawer_enabled)}
              disabled={!mayManage}
              onChange={(e) => setDraft({ ...draft, drawer_enabled: e.target.checked })}
            />
            <Checkbox
              label={t('nx.dev.requireCustomer')}
              hint={t('nx.dev.requireCustomerHint')}
              checked={Boolean(draft.require_customer)}
              disabled={!mayManage}
              onChange={(e) => setDraft({ ...draft, require_customer: e.target.checked })}
            />
          </div>

          {mayManage ? (
            <div className="mt-4">
              <Button
                variant="primary"
                busy={busy}
                busyLabel={t('nx.dev.savingSettings')}
                onClick={() => void save()}
              >
                {t('nx.dev.saveSettings')}
              </Button>
            </div>
          ) : null}
        </>
      ) : null}
    </Panel>
  );
}

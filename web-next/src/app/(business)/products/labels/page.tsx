'use client';

// Barcodes and the tags they go on.
//
// # Changing the scheme changes nothing already printed
//
// The route says it plainly: "a code printed on nine hundred hang tags does not
// move because somebody edited a setting". So this screen is about what the
// NEXT code will look like, and says so above the form — an owner who thinks
// they have just renumbered their stock is an owner about to reprint a shop.
//
// The example comes from the server. Building one here would be a second
// implementation of the rule, free to disagree with the one that actually mints
// the codes.
//
// # A print run has to name something
//
// An empty selection reads as "every product in the shop", which is a
// reasonable reading and a very expensive mistake in labels and time. The run
// is refused here rather than sent.
//
// # A roll is not a sheet
//
// Thermal stock has no grid, so it reports no per-sheet figure rather than 1 —
// "1 per sheet" invites somebody to work out how many sheets they need for a
// roll.
//
// # `label.print` is what gets you in; `label.manage` is what lets you change
//
// Every read this screen makes is gated on print, so gating the way IN on
// either-of would let a custom role holding only manage through to a screen
// that 403s on its first request. Manage hides the editing controls, not the
// page.

import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { SCHEME_PARTS, SYMBOLOGIES, type BarcodeScheme } from '@/lib/devices/hardware';
import type { LabelTemplate } from '@/lib/labels/studio';
import { useT, type Key } from '@/lib/i18n/locale';

import { PrintRun } from './run';
import { TemplateEditor } from './templates';

const PART_LABEL: Record<string, Key> = {
  category: 'nx.lbl.pCategory',
  brand: 'nx.lbl.pBrand',
  colour: 'nx.lbl.pColour',
  size: 'nx.lbl.pSize',
  sequence: 'nx.lbl.pSequence',
};

function LabelsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('label.manage');

  const scheme = useApi<BarcodeScheme>(
    scope ? '/labels/scheme' : null,
    scope ?? undefined,
  );
  const templates = useApiList<LabelTemplate>(
    scope ? '/labels/templates' : null,
    scope ?? undefined,
  );

  const [draft, setDraft] = useState<BarcodeScheme | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    if (scheme.data) setDraft(scheme.data);
  }, [scheme.data]);

  const rows = templates.data?.data ?? [];

  async function saveScheme() {
    if (!scope || !draft) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    setNote(null);
    try {
      await api.put(`/labels/scheme?company_id=${scope.company_id}`, {
        parts: draft.parts,
        separator: draft.separator,
        symbology: draft.symbology,
        part_length: draft.part_length,
      });
      setNote(t('nx.lbl.schemeSaved'));
      void scheme.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function generate() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const out = await api.post<{ generated?: number }>(
        `/labels/barcodes?company_id=${scope.company_id}`,
        {},
      );
      // A variant that already has a code keeps it, so the count is what
      // actually changed rather than how many products exist.
      setNote(t('nx.lbl.generated', { count: String(out.generated ?? 0) }));
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title={t('nx.lbl.title')} description={t('nx.lbl.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {scheme.error ? (
        <ErrorState error={scheme.error} onRetry={() => void scheme.refetch()} />
      ) : null}

      {draft ? (
        <Panel
          className="mb-5"
          title={t('nx.lbl.schemeTitle')}
          description={t('nx.lbl.schemeHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              name="parts"
              label={t('nx.lbl.parts')}
              hint={t('nx.lbl.partsHint')}
              className="sm:col-span-2"
            >
              <span className="flex flex-wrap gap-3 pt-1">
                {SCHEME_PARTS.map((part) => (
                  <label key={part} className="flex items-center gap-2 text-body">
                    <input
                      type="checkbox"
                      checked={draft.parts.includes(part)}
                      disabled={!mayManage}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          parts: e.target.checked
                            ? [...draft.parts, part]
                            : draft.parts.filter((p) => p !== part),
                        })
                      }
                      className="size-4 rounded-xs border border-input accent-primary"
                    />
                    {t(PART_LABEL[part] as Key)}
                  </label>
                ))}
              </span>
            </Field>
            <Field name="separator" label={t('nx.lbl.separator')}>
              <Input
                value={draft.separator}
                onChange={(e) => setDraft({ ...draft, separator: e.target.value })}
                maxLength={1}
                disabled={!mayManage}
                className="num"
              />
            </Field>
            <Field name="part_length" label={t('nx.lbl.partLength')}>
              <Input
                value={String(draft.part_length)}
                onChange={(e) =>
                  setDraft({ ...draft, part_length: Number(e.target.value) || 0 })
                }
                inputMode="numeric"
                disabled={!mayManage}
                className="num text-end"
              />
            </Field>
            <Field
              name="symbology"
              label={t('nx.lbl.symbology')}
              hint={t('nx.lbl.symbologyHint')}
            >
              <Select
                value={draft.symbology}
                onChange={(e) => setDraft({ ...draft, symbology: e.target.value })}
                disabled={!mayManage}
              >
                {SYMBOLOGIES.map((sym) => (
                  <option key={sym} value={sym}>
                    {sym}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="sm:col-span-2">
              <p className="text-label text-muted">{t('nx.lbl.example')}</p>
              {/* The server's own example. Building one here would be a second
                  implementation of the rule that mints the codes. */}
              <p className="num mt-0.5 text-card-title font-semibold">
                {scheme.data?.example ?? '—'}
              </p>
            </div>
          </div>

          {/* Said before the button, not after. */}
          <p className="mt-4 max-w-prose text-caption text-caution-fg">
            {t('nx.lbl.schemeWarning')}
          </p>

          {mayManage ? (
            <div className="mt-4 flex flex-wrap gap-3">
              <Button variant="primary" disabled={busy} onClick={() => void saveScheme()}>
                {t('nx.lbl.saveScheme')}
              </Button>
              <Button disabled={busy} onClick={() => void generate()}>
                {t('nx.lbl.generate')}
              </Button>
            </div>
          ) : null}
        </Panel>
      ) : null}

      {/* The layouts, and the editor that was missing: `POST` had a screen
          and `PUT` and `DELETE` had none, so a shop that got the height wrong
          on its roll could add a fourth layout and never correct the three it
          had. */}
      {scope ? (
        <TemplateEditor
          companyId={scope.company_id}
          templates={rows}
          isLoading={templates.isLoading}
          mayManage={mayManage}
          onChanged={() => void templates.refetch()}
        />
      ) : null}

      {/* What a run will actually print, and the manual barcode override,
          which needs a variant id and the code it currently carries — and this
          is the only list in the product that has both. */}
      {scope && rows.length > 0 ? (
        <PrintRun
          companyId={scope.company_id}
          currency={currency}
          market={market}
          templates={rows}
          mayManage={mayManage}
        />
      ) : null}

    </>
  );
}

export default function LabelsPage() {
  return (
    <RequirePermission anyOf={['label.print']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <LabelsScreen />
      </Suspense>
    </RequirePermission>
  );
}

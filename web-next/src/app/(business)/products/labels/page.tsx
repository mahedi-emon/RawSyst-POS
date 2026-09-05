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

import { Tags } from 'lucide-react';
import { Suspense, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import {
  perSheet,
  printProblem,
  SCHEME_PARTS,
  SYMBOLOGIES,
  type BarcodeScheme,
  type LabelTemplate,
} from '@/lib/devices/hardware';
import { useT, type Key } from '@/lib/i18n/locale';

const PART_LABEL: Record<string, Key> = {
  category: 'nx.lbl.pCategory',
  brand: 'nx.lbl.pBrand',
  colour: 'nx.lbl.pColour',
  size: 'nx.lbl.pSize',
  sequence: 'nx.lbl.pSequence',
};

const PROBLEM: Record<string, Key> = {
  no_template: 'nx.lbl.needTemplate',
  nothing_selected: 'nx.lbl.needSelection',
  no_copies: 'nx.lbl.needCopies',
};

function LabelsScreen() {
  const t = useT();
  const scope = useCompanyScope();
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
  const [templateID, setTemplateID] = useState('');
  const [search, setSearch] = useState('');
  const [copies, setCopies] = useState('1');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    if (scheme.data) setDraft(scheme.data);
  }, [scheme.data]);

  const rows = templates.data?.data ?? [];
  const state = printProblem({
    templateID,
    variantIDs: [],
    categoryID: '',
    brandID: '',
    search,
    copies,
  });

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

  async function print() {
    if (!scope || state !== 'none') return;
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const out = await api.post<{ labels?: number; count?: number }>(
        `/labels/print?company_id=${scope.company_id}`,
        {
          template_id: templateID,
          search: search.trim(),
          copies: Number(copies),
        },
      );
      setNote(
        t('nx.lbl.printed', { count: String(out.labels ?? out.count ?? 0) }),
      );
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<LabelTemplate>[] = [
    {
      key: 'name',
      header: t('nx.lbl.colTemplate'),
      primary: true,
      cell: (tpl) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{tpl.name}</span>
          <span className="text-caption text-muted">
            {tpl.kind === 'thermal' ? t('nx.lbl.roll') : t('nx.lbl.sheet')}
          </span>
        </span>
      ),
    },
    {
      key: 'size',
      header: t('nx.lbl.colSize'),
      width: 'w-36',
      cell: (tpl) => (
        <span className="num">
          {tpl.width_mm} × {tpl.height_mm} mm
        </span>
      ),
    },
    {
      key: 'perSheet',
      header: t('nx.lbl.colPerSheet'),
      numeric: true,
      width: 'w-32',
      cell: (tpl) => {
        const n = perSheet(tpl);
        // A roll does not come in sheets.
        return n === null ? (
          <span className="text-muted">—</span>
        ) : (
          <span className="num">{n}</span>
        );
      },
    },
    {
      key: 'fields',
      header: t('nx.lbl.colFields'),
      secondary: true,
      cell: (tpl) => (
        <span className="num text-caption text-muted">
          {tpl.fields.map((f) => f.field).join(', ') || '—'}
        </span>
      ),
    },
    {
      key: 'default',
      header: t('nx.lbl.colDefault'),
      width: 'w-28',
      cell: (tpl) =>
        tpl.is_default ? <Badge tone="info">{t('nx.lbl.isDefault')}</Badge> : null,
    },
  ];

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

      <h2 className="mt-8 mb-3 text-card-title font-semibold text-fg">
        {t('nx.lbl.templatesTitle')}
      </h2>
      {templates.isLoading && !templates.data ? <TableSkeleton columns={5} /> : null}
      {!templates.isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Tags}
          title={t('nx.lbl.noTemplatesTitle')}
          description={t('nx.lbl.noTemplatesDesc')}
        />
      ) : null}
      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.lbl.templatesTitle')}
          columns={columns}
          rows={rows}
          rowKey={(tpl) => tpl.id}
        />
      ) : null}

      {rows.length > 0 ? (
        <Panel
          className="mt-6"
          title={t('nx.lbl.printTitle')}
          description={t('nx.lbl.printHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4 lg:items-end">
            <Field name="template_id" label={t('nx.lbl.template')} required>
              <Select
                value={templateID}
                onChange={(e) => setTemplateID(e.target.value)}
              >
                <option value="">{t('nx.lbl.chooseTemplate')}</option>
                {rows.map((tpl) => (
                  <option key={tpl.id} value={tpl.id}>
                    {tpl.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="search"
              label={t('nx.lbl.which')}
              hint={t('nx.lbl.whichHint')}
              required
            >
              <Input value={search} onChange={(e) => setSearch(e.target.value)} />
            </Field>
            <Field name="copies" label={t('nx.lbl.copies')}>
              <Input
                value={copies}
                onChange={(e) => setCopies(e.target.value)}
                inputMode="numeric"
                className="num text-end"
              />
            </Field>
            <Button
              variant="primary"
              disabled={busy || state !== 'none'}
              onClick={() => void print()}
            >
              {t('nx.lbl.print')}
            </Button>
          </div>
          {state !== 'none' ? (
            <p className="mt-3 text-caption text-muted">{t(PROBLEM[state] as Key)}</p>
          ) : null}
        </Panel>
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

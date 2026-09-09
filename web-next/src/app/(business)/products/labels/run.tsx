'use client';

// The print run, what it will actually print, and overriding one code by hand.
//
// # A run you can see before you print it
//
// `POST /labels/print` assembles a run and answers with every label in it —
// sku, name, barcode, symbology and the VAT-inclusive price. The screen used to
// send that request and report a count, which is the one thing about a print
// run nobody needs to know: what a person wants before committing nine hundred
// tags to a rail is to look at three of them.
//
// It is a POST that writes nothing. The route says so in as many words: "a POST
// although it writes nothing: a selection can name several hundred variant ids
// and a query string that long is one a proxy will truncate."
//
// # Which is also where a barcode gets overridden
//
// `PUT /labels/barcodes/{variantID}` is B3's manual override, for goods that
// arrive carrying a code somebody else assigned — a manufacturer's EAN on a
// bought-in product. It needs a variant id and the code it currently carries,
// and this list is the only place in the product that has both. Building a
// separate product picker for it would be a second way to find the same row.
//
// # The duplicate check here is a courtesy, not the rule
//
// `variant_barcode_uq` is what makes a barcode unique, and the server's refusal
// is the authority. The check against what is on screen catches the obvious
// case — the same code typed onto two variants of one shirt — before somebody
// presses the button.

import { ScanBarcode } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { formatMoney, type MarketCode } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  barcodeProblem,
  type LabelTemplate,
  type PreparedLabel,
  type PreparedSheet,
} from '@/lib/labels/studio';

const PROBLEM: Record<string, Key> = {
  empty: 'nx.lbl.codeEmpty',
  unchanged: 'nx.lbl.codeUnchanged',
  taken_here: 'nx.lbl.codeTakenHere',
};

export function PrintRun({
  companyId,
  currency,
  market,
  templates,
  mayManage,
}: {
  companyId: string;
  currency: string;
  market: MarketCode;
  templates: LabelTemplate[];
  mayManage: boolean;
}) {
  const t = useT();

  const [templateID, setTemplateID] = useState('');
  const [search, setSearch] = useState('');
  const [copies, setCopies] = useState('1');
  const [sheet, setSheet] = useState<PreparedSheet | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});

  // The override, held one at a time: two open editors would let somebody save
  // two codes from one reading of the list.
  const [overriding, setOverriding] = useState<PreparedLabel | null>(null);
  const [code, setCode] = useState('');
  const [reason, setReason] = useState('');
  const [note, setNote] = useState<string | null>(null);

  const chosen = templates.find((tpl) => tpl.id === templateID) ?? null;
  const labels = sheet?.labels ?? [];
  const copiesN = Number(copies);
  const runnable =
    templateID !== '' && search.trim() !== '' && Number.isInteger(copiesN) && copiesN > 0;

  const problem = overriding
    ? barcodeProblem(code, overriding.barcode, labels, overriding.variant_id)
    : null;

  async function prepare() {
    if (!runnable) return;
    setBusy(true);
    setError(null);
    setFields({});
    setNote(null);
    setOverriding(null);
    try {
      const out = await api.post<PreparedSheet>(`/labels/print?company_id=${companyId}`, {
        template_id: templateID,
        search: search.trim(),
        copies: copiesN,
      });
      setSheet(out);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  function beginOverride(label: PreparedLabel) {
    setOverriding(label);
    setCode(label.barcode);
    setReason('');
    setError(null);
    setNote(null);
  }

  async function override() {
    if (!overriding || problem) return;
    setBusy(true);
    setError(null);
    setFields({});
    try {
      await api.put(
        `/labels/barcodes/${overriding.variant_id}?company_id=${companyId}`,
        { barcode: code.trim(), reason: reason.trim() },
      );
      setNote(
        t('nx.lbl.codeSet', { sku: overriding.sku, code: code.trim() }),
      );
      setOverriding(null);
      // Re-read the run, so the list shows the code the till will now scan
      // rather than the one that was there when the run was assembled.
      await prepareQuietly();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  /** The same read, without clearing the success note the override just set. */
  async function prepareQuietly() {
    if (!runnable) return;
    try {
      const out = await api.post<PreparedSheet>(`/labels/print?company_id=${companyId}`, {
        template_id: templateID,
        search: search.trim(),
        copies: copiesN,
      });
      setSheet(out);
    } catch {
      // The override succeeded; a failed re-read is not worth replacing that
      // message with. The list is refreshed the next time a run is prepared.
    }
  }

  const columns: Column<PreparedLabel>[] = [
    {
      key: 'product',
      header: t('nx.lbl.colProduct'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{l.name}</span>
          <span className="num text-caption text-muted">
            {l.sku}
            {l.attributes ? ` · ${l.attributes}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'barcode',
      header: t('nx.lbl.colBarcode'),
      cell: (l) =>
        l.barcode ? (
          <span className="flex flex-col gap-0.5">
            <span className="num">{l.barcode}</span>
            {/* For a digit symbology the readable string is not the barcode,
                so both are shown rather than one standing in for the other. */}
            {l.readable && l.readable !== l.barcode ? (
              <span className="num text-caption text-muted">{l.readable}</span>
            ) : null}
          </span>
        ) : (
          <Badge tone="caution">{t('nx.lbl.noCode')}</Badge>
        ),
    },
    {
      key: 'symbology',
      header: t('nx.lbl.colSymbology'),
      secondary: true,
      width: 'w-32',
      cell: (l) => <span className="num text-muted">{l.symbology}</span>,
    },
    {
      key: 'price',
      header: t('nx.lbl.colPrice'),
      numeric: true,
      width: 'w-36',
      cell: (l) => formatMoney(l.price, { currency: l.currency || currency, market }),
    },
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.lbl.colAct'),
            width: 'w-32',
            cell: (l: PreparedLabel) => (
              <Button size="sm" variant="ghost" onClick={() => beginOverride(l)}>
                {t('nx.lbl.override')}
              </Button>
            ),
          },
        ]
      : []),
  ];

  return (
    <section className="mt-8">
      <h2 className="mb-1 text-card-title font-semibold text-fg">
        {t('nx.lbl.runTitle')}
      </h2>
      <p className="mb-3 max-w-prose text-caption text-muted">{t('nx.lbl.runHint')}</p>

      <FormError message={error} fields={fields} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      <Panel>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4 lg:items-end">
          <Field name="template_id" label={t('nx.lbl.template')} required>
            <Select value={templateID} onChange={(e) => setTemplateID(e.target.value)}>
              <option value="">{t('nx.lbl.chooseTemplate')}</option>
              {templates.map((tpl) => (
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
              numeric
              inputMode="numeric"
              value={copies}
              onChange={(e) => setCopies(e.target.value)}
            />
          </Field>
          <Button
            variant="primary"
            busy={busy}
            busyLabel={t('nx.lbl.preparing')}
            disabled={!runnable}
            onClick={() => void prepare()}
          >
            {t('nx.lbl.prepare')}
          </Button>
        </div>
        {!runnable ? (
          <p className="mt-3 text-caption text-muted">
            {templateID === ''
              ? t('nx.lbl.needTemplate')
              : search.trim() === ''
                ? t('nx.lbl.needSelection')
                : t('nx.lbl.needCopies')}
          </p>
        ) : null}
      </Panel>

      {overriding ? (
        <Panel
          className="mt-5"
          title={t('nx.lbl.overrideTitle', { sku: overriding.sku })}
          description={t('nx.lbl.overrideHint')}
        >
          <dl className="grid gap-3 sm:grid-cols-3">
            <div>
              <dt className="text-label text-muted">{t('nx.lbl.overrideProduct')}</dt>
              <dd className="mt-0.5 text-body text-fg">{overriding.name}</dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.lbl.overrideVariant')}</dt>
              <dd className="num mt-0.5 text-body text-fg">
                {overriding.attributes || overriding.sku}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.lbl.overrideCurrent')}</dt>
              <dd className="num mt-0.5 text-body text-fg">
                {overriding.barcode || t('nx.lbl.noCode')}
              </dd>
            </div>
          </dl>

          <div className="mt-4 grid max-w-prose gap-4">
            <Field
              name="barcode"
              label={t('nx.lbl.newCode')}
              hint={t('nx.lbl.newCodeHint')}
              error={
                problem && problem !== 'unchanged' ? t(PROBLEM[problem] as Key) : fields.barcode
              }
              required
            >
              <Input
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                dir="ltr"
                className="num"
              />
            </Field>
            <Field name="reason" label={t('nx.lbl.codeReason')} hint={t('nx.lbl.codeReasonHint')}>
              <Textarea rows={2} value={reason} onChange={(e) => setReason(e.target.value)} />
            </Field>
          </div>

          <p className="mt-3 max-w-prose text-body text-caution-fg">
            {t('nx.lbl.overrideWarning')}
          </p>

          <div className="mt-4 flex flex-wrap gap-3">
            <Button
              variant="primary"
              busy={busy}
              busyLabel={t('nx.lbl.saving')}
              disabled={problem !== null}
              onClick={() => void override()}
            >
              {t('nx.lbl.setCode')}
            </Button>
            <Button variant="ghost" onClick={() => setOverriding(null)}>
              {t('nx.lbl.cancel')}
            </Button>
            {problem === 'unchanged' ? (
              <p className="self-center text-caption text-muted">
                {t('nx.lbl.codeUnchanged')}
              </p>
            ) : null}
          </div>
        </Panel>
      ) : null}

      {sheet && labels.length === 0 ? (
        <EmptyState
          className="mt-5"
          icon={ScanBarcode}
          title={t('nx.lbl.noMatchTitle')}
          description={t('nx.lbl.noMatchDesc')}
        />
      ) : null}

      {labels.length > 0 ? (
        <>
          <p className="mt-5 mb-3 text-body">
            {t('nx.lbl.runReady', {
              count: String(labels.length),
              template: chosen?.name ?? sheet?.template.name ?? '',
            })}
          </p>
          <DataTable
            caption={t('nx.lbl.runTitle')}
            columns={columns}
            rows={labels}
            // A run repeats each label, so a variant id alone is not unique.
            rowKey={(l, i) => `${l.variant_id}-${i}`}
            isSelected={(l) => l.variant_id === overriding?.variant_id}
          />
        </>
      ) : null}
    </section>
  );
}

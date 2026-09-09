'use client';

// The label template editor.
//
// # Why this exists
//
// `POST /labels/templates` had a screen and `PUT` and `DELETE` had none. A shop
// that got the height wrong on its thermal roll — 25mm ordered, 30mm delivered
// — could create a fourth layout and never correct or remove the three it had.
// Three routes, one of them reachable.
//
// # A preview, not a mock editor
//
// The box on the right is drawn at the template's own millimetres, and it shows
// exactly the fields the print run will fill, in order, at their own sizes. It
// is a proof of the layout rather than a rendering of the barcode: the bars a
// thermal printer produces come from its own driver, and drawing a fake Code
// 128 here would be a picture of a code that does not scan.
//
// # The field list is the server's
//
// `labels.PrintableFields` names seven things a run can fill and the route
// refuses anything else. The editor offers exactly those, narrowed by kind, so
// a layout saved here is one the printer can render. Adding an eighth means
// adding it to the label the server builds, not to a list on a screen.
//
// # Removing one is asked twice
//
// A deleted layout takes the print button that named it with it, and there is
// no undo. The confirmation is inline and says which layout, because a dialog
// that says "are you sure" and nothing else is a dialog people click through.

import { Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  blankDraft,
  draftOf,
  fieldsFor,
  isTextField,
  perSheet,
  templateBody,
  templateProblem,
  LABEL_KINDS,
  type LabelKind,
  type LabelTemplate,
  type TemplateDraft,
} from '@/lib/labels/studio';
import { cn } from '@/lib/utils';

const KIND_LABEL: Record<string, Key> = {
  hang_tag: 'nx.lbl.kindHangTag',
  thermal: 'nx.lbl.kindThermal',
  a4_sheet: 'nx.lbl.kindA4',
  loyalty_card: 'nx.lbl.kindCard',
};

const FIELD_LABEL: Record<string, Key> = {
  logo: 'nx.lbl.fLogo',
  name: 'nx.lbl.fName',
  name_ar: 'nx.lbl.fNameAr',
  attributes: 'nx.lbl.fAttributes',
  price: 'nx.lbl.fPrice',
  barcode: 'nx.lbl.fBarcode',
  customer_name: 'nx.lbl.fCustomerName',
};

const PROBLEM: Record<string, Key> = {
  no_name: 'nx.lbl.needName',
  unknown_kind: 'nx.lbl.needKind',
  bad_width: 'nx.lbl.needWidth',
  bad_height: 'nx.lbl.needHeight',
  needs_grid: 'nx.lbl.needGrid',
  no_fields: 'nx.lbl.needFields',
  unprintable_field: 'nx.lbl.unprintableField',
};

export function TemplateEditor({
  companyId,
  templates,
  isLoading,
  mayManage,
  onChanged,
}: {
  companyId: string;
  templates: LabelTemplate[];
  isLoading: boolean;
  mayManage: boolean;
  onChanged: () => void;
}) {
  const t = useT();

  const [draft, setDraft] = useState<TemplateDraft | null>(null);
  const [removing, setRemoving] = useState<LabelTemplate | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [note, setNote] = useState<string | null>(null);

  const problem = draft ? templateProblem(draft) : null;

  function begin(next: TemplateDraft) {
    setDraft(next);
    setRemoving(null);
    setError(null);
    setFields({});
    setNote(null);
  }

  async function save() {
    if (!draft || problem) return;
    setBusy(true);
    setError(null);
    setFields({});
    try {
      const body = templateBody(draft);
      if (draft.id) {
        await api.put(`/labels/templates/${draft.id}?company_id=${companyId}`, body);
      } else {
        await api.post(`/labels/templates?company_id=${companyId}`, body);
      }
      setNote(t('nx.lbl.templateSaved', { name: draft.name.trim() }));
      setDraft(null);
      onChanged();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!removing) return;
    setBusy(true);
    setError(null);
    try {
      await api.delete(`/labels/templates/${removing.id}?company_id=${companyId}`);
      setNote(t('nx.lbl.templateRemoved', { name: removing.name }));
      setRemoving(null);
      if (draft?.id === removing.id) setDraft(null);
      onChanged();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  /** Adds a line the layout does not already carry. */
  function addField(field: string) {
    if (!draft) return;
    if (draft.fields.some((f) => f.field === field)) return;
    setDraft({
      ...draft,
      fields: [
        ...draft.fields,
        isTextField(field) ? { field, size: 8 } : { field, height: 10 },
      ],
    });
  }

  function updateField(index: number, patch: Partial<TemplateDraft['fields'][number]>) {
    if (!draft) return;
    setDraft({
      ...draft,
      fields: draft.fields.map((f, i) => (i === index ? { ...f, ...patch } : f)),
    });
  }

  function moveField(index: number, by: number) {
    if (!draft) return;
    const next = [...draft.fields];
    const target = index + by;
    if (target < 0 || target >= next.length) return;
    const [line] = next.splice(index, 1);
    if (line) next.splice(target, 0, line);
    setDraft({ ...draft, fields: next });
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
            {t(KIND_LABEL[tpl.kind] ?? 'nx.lbl.kindThermal')}
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
        // A roll does not come in sheets, and "1 per sheet" would invite
        // somebody to work out how many sheets they need for one.
        return n === null ? <span className="text-muted">—</span> : <span className="num">{n}</span>;
      },
    },
    {
      key: 'fields',
      header: t('nx.lbl.colFields'),
      secondary: true,
      cell: (tpl) => (
        <span className="text-caption text-muted">
          {tpl.fields.map((f) => t(FIELD_LABEL[f.field] ?? 'nx.lbl.fName')).join(', ') ||
            '—'}
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
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.lbl.colAct'),
            width: 'w-40',
            cell: (tpl: LabelTemplate) => (
              <span className="flex gap-1">
                <Button size="sm" variant="ghost" onClick={() => begin(draftOf(tpl))}>
                  {t('nx.lbl.edit')}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setRemoving(tpl);
                    setDraft(null);
                  }}
                >
                  <Trash2 aria-hidden="true" />
                  <span className="sr-only">
                    {t('nx.lbl.removeOne', { name: tpl.name })}
                  </span>
                </Button>
              </span>
            ),
          },
        ]
      : []),
  ];

  return (
    <section className="mt-8">
      <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h2 className="text-card-title font-semibold text-fg">
            {t('nx.lbl.templatesTitle')}
          </h2>
          <p className="mt-0.5 max-w-prose text-caption text-muted">
            {t('nx.lbl.templatesHint')}
          </p>
        </div>
        {mayManage ? (
          <Button variant="secondary" onClick={() => begin(blankDraft('thermal'))}>
            <Plus aria-hidden="true" />
            {t('nx.lbl.newTemplate')}
          </Button>
        ) : null}
      </div>

      <FormError message={error} fields={fields} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {removing ? (
        <Panel
          className="mb-5"
          title={t('nx.lbl.removeTitle', { name: removing.name })}
          description={t('nx.lbl.removeHint')}
        >
          <div className="flex flex-wrap gap-3">
            <Button variant="destructive" busy={busy} onClick={() => void remove()}>
              {t('nx.lbl.confirmRemove')}
            </Button>
            <Button variant="ghost" onClick={() => setRemoving(null)}>
              {t('nx.lbl.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {draft ? (
        <Panel
          className="mb-5"
          title={draft.id ? t('nx.lbl.editTitle') : t('nx.lbl.newTitle')}
          description={t('nx.lbl.editHint')}
        >
          <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_16rem]">
            <div>
              <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                <Field
                  name="name"
                  label={t('nx.lbl.templateName')}
                  hint={t('nx.lbl.templateNameHint')}
                  error={fields.name}
                  required
                  className="sm:col-span-2"
                >
                  <Input
                    value={draft.name}
                    onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                  />
                </Field>
                <Field name="kind" label={t('nx.lbl.kind')}>
                  <Select
                    value={draft.kind}
                    onChange={(e) => {
                      // Changing the kind changes which fields make sense and
                      // whether a grid applies, so the draft is rebuilt at the
                      // new kind rather than carrying a loyalty card's fields
                      // onto a hang tag.
                      const kind = e.target.value as LabelKind;
                      setDraft({ ...blankDraft(kind), id: draft.id, name: draft.name });
                    }}
                  >
                    {LABEL_KINDS.map((k) => (
                      <option key={k} value={k}>
                        {t(KIND_LABEL[k] ?? 'nx.lbl.kindThermal')}
                      </option>
                    ))}
                  </Select>
                </Field>

                <Field name="width_mm" label={t('nx.lbl.width')} error={fields.width_mm}>
                  <Input
                    numeric
                    inputMode="decimal"
                    value={draft.width_mm}
                    onChange={(e) => setDraft({ ...draft, width_mm: e.target.value })}
                  />
                </Field>
                <Field name="height_mm" label={t('nx.lbl.height')} error={fields.height_mm}>
                  <Input
                    numeric
                    inputMode="decimal"
                    value={draft.height_mm}
                    onChange={(e) => setDraft({ ...draft, height_mm: e.target.value })}
                  />
                </Field>
                <Field name="margin_mm" label={t('nx.lbl.margin')}>
                  <Input
                    numeric
                    inputMode="decimal"
                    value={draft.margin_mm}
                    onChange={(e) => setDraft({ ...draft, margin_mm: e.target.value })}
                  />
                </Field>

                {draft.kind === 'a4_sheet' ? (
                  <>
                    <Field
                      name="columns"
                      label={t('nx.lbl.across')}
                      error={fields.columns}
                    >
                      <Input
                        numeric
                        inputMode="numeric"
                        value={draft.columns}
                        onChange={(e) => setDraft({ ...draft, columns: e.target.value })}
                      />
                    </Field>
                    <Field name="rows" label={t('nx.lbl.down')}>
                      <Input
                        numeric
                        inputMode="numeric"
                        value={draft.rows}
                        onChange={(e) => setDraft({ ...draft, rows: e.target.value })}
                      />
                    </Field>
                    <Field name="gap_mm" label={t('nx.lbl.gap')}>
                      <Input
                        numeric
                        inputMode="decimal"
                        value={draft.gap_mm}
                        onChange={(e) => setDraft({ ...draft, gap_mm: e.target.value })}
                      />
                    </Field>
                  </>
                ) : null}
              </div>

              <div className="mt-4">
                <Checkbox
                  label={t('nx.lbl.makeDefault')}
                  hint={t('nx.lbl.makeDefaultHint')}
                  checked={draft.is_default}
                  onChange={(e) => setDraft({ ...draft, is_default: e.target.checked })}
                />
              </div>

              <h3 className="mt-6 mb-2 text-body font-semibold text-fg">
                {t('nx.lbl.whatItPrints')}
              </h3>
              <ul className="flex flex-col divide-y divide-line rounded-sm border border-line">
                {draft.fields.map((line, i) => (
                  <li key={line.field} className="flex flex-wrap items-end gap-3 p-3">
                    <span className="min-w-32 flex-1 text-body text-fg">
                      {t(FIELD_LABEL[line.field] ?? 'nx.lbl.fName')}
                    </span>
                    {isTextField(line.field) ? (
                      <span className="w-24">
                        <Field label={t('nx.lbl.pointSize')}>
                          <Input
                            numeric
                            inputMode="numeric"
                            value={String(line.size ?? '')}
                            onChange={(e) =>
                              updateField(i, { size: Number(e.target.value) || undefined })
                            }
                          />
                        </Field>
                      </span>
                    ) : line.field === 'barcode' ? (
                      <span className="w-24">
                        <Field label={t('nx.lbl.barHeight')}>
                          <Input
                            numeric
                            inputMode="numeric"
                            value={String(line.height ?? '')}
                            onChange={(e) =>
                              updateField(i, {
                                height: Number(e.target.value) || undefined,
                              })
                            }
                          />
                        </Field>
                      </span>
                    ) : null}
                    {isTextField(line.field) ? (
                      <Checkbox
                        label={t('nx.lbl.bold')}
                        checked={Boolean(line.bold)}
                        onChange={(e) => updateField(i, { bold: e.target.checked })}
                      />
                    ) : null}
                    <span className="flex gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={i === 0}
                        onClick={() => moveField(i, -1)}
                        aria-label={t('nx.lbl.moveUp')}
                      >
                        ↑
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={i === draft.fields.length - 1}
                        onClick={() => moveField(i, 1)}
                        aria-label={t('nx.lbl.moveDown')}
                      >
                        ↓
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          setDraft({
                            ...draft,
                            fields: draft.fields.filter((_, at) => at !== i),
                          })
                        }
                        aria-label={t('nx.lbl.removeLine')}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    </span>
                  </li>
                ))}
              </ul>

              <div className="mt-3 flex flex-wrap gap-2">
                {fieldsFor(draft.kind)
                  .filter((f) => !draft.fields.some((line) => line.field === f))
                  .map((f) => (
                    <Button key={f} size="sm" variant="secondary" onClick={() => addField(f)}>
                      <Plus aria-hidden="true" />
                      {t(FIELD_LABEL[f] ?? 'nx.lbl.fName')}
                    </Button>
                  ))}
              </div>
            </div>

            {/* The layout at its own millimetres. A proof of the arrangement,
                not a rendering of the barcode: the bars come from the printer
                driver, and drawing a fake code here would be a picture of
                something that does not scan. */}
            <div>
              <p className="mb-2 text-label text-muted">{t('nx.lbl.preview')}</p>
              <div
                className="rounded-sm border border-dashed border-line-strong bg-surface p-2"
                style={{
                  inlineSize: `${Math.min(Number(draft.width_mm) || 50, 90)}mm`,
                  blockSize: `${Math.min(Number(draft.height_mm) || 25, 90)}mm`,
                }}
                aria-hidden="true"
              >
                <div
                  className="flex h-full flex-col justify-center gap-0.5 overflow-hidden"
                  style={{ padding: `${Number(draft.margin_mm) || 0}mm` }}
                >
                  {draft.fields.map((line) => (
                    <span
                      key={line.field}
                      className={cn(
                        'truncate text-fg',
                        line.bold && 'font-semibold',
                        line.field === 'name_ar' && 'text-end',
                      )}
                      style={
                        line.field === 'barcode'
                          ? {
                              blockSize: `${line.height ?? 10}mm`,
                              background:
                                'repeating-linear-gradient(90deg, var(--ry-text) 0 1px, transparent 1px 3px)',
                            }
                          : { fontSize: `${line.size ?? 8}pt` }
                      }
                    >
                      {line.field === 'barcode' ? '' : t(FIELD_LABEL[line.field] ?? 'nx.lbl.fName')}
                    </span>
                  ))}
                </div>
              </div>
              <p className="mt-2 text-caption text-muted">{t('nx.lbl.previewNote')}</p>
            </div>
          </div>

          {problem ? (
            <p className="mt-4 text-caption text-muted">{t(PROBLEM[problem] as Key)}</p>
          ) : null}

          <div className="mt-4 flex flex-wrap gap-3">
            <Button
              variant="primary"
              busy={busy}
              busyLabel={t('nx.lbl.saving')}
              disabled={problem !== null}
              onClick={() => void save()}
            >
              {t('nx.lbl.saveTemplate')}
            </Button>
            <Button variant="ghost" onClick={() => setDraft(null)}>
              {t('nx.lbl.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && templates.length === 0 ? <TableSkeleton columns={5} /> : null}

      {!isLoading && templates.length === 0 ? (
        <EmptyState
          icon={Plus}
          title={t('nx.lbl.noTemplatesTitle')}
          description={t('nx.lbl.noTemplatesDesc')}
          action={
            mayManage ? (
              <Button variant="primary" onClick={() => begin(blankDraft('thermal'))}>
                {t('nx.lbl.newTemplate')}
              </Button>
            ) : undefined
          }
        />
      ) : null}

      {templates.length > 0 ? (
        <DataTable
          caption={t('nx.lbl.templatesTitle')}
          columns={columns}
          rows={templates}
          rowKey={(tpl) => tpl.id}
          isSelected={(tpl) => tpl.id === draft?.id || tpl.id === removing?.id}
        />
      ) : null}
    </section>
  );
}

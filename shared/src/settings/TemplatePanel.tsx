// Document templates, blueprint I2 / P35.
//
// Lives under Branding rather than in a screen of its own, because it is the
// same question a client is answering: what do my documents look like. The logo
// is the picture and this is the words.
//
// # Per document type, because I2 says so
//
// A credit note usually wants a different footer from an invoice — one is an
// apology and the other is a demand — and a simplified receipt for a bottle of
// water wants neither. Only the four types this product actually issues are
// offered; a template for a document that cannot be printed is configuration
// for nothing.
//
// # It cannot change what a document recorded
//
// Every field here is presentation. None of it reaches a figure, a party, a tax
// number or a date, and the copy says so — a client changing a footer needs to
// know they are not amending last quarter's invoices, and a client who thinks
// they might be will never touch the screen.

import { useCallback, useEffect, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import {
  fetchTemplates,
  resetTemplate,
  saveTemplate,
  type DocType,
  type DocumentTemplate,
} from '../api/branding';
import { Field, FormError, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

/** How each type reads, and what it is for. The labels are the shop's words
 *  rather than the column's: nobody outside this codebase says "standard". */
function documentTypes(
  t: (key: Key) => string,
): Array<{ key: DocType; label: string; purpose: string }> {
  return [
    {
      key: 'standard',
      label: t('tmpl.taxInvoice'),
      purpose: t('tmpl.taxInvoiceHint'),
    },
    {
      key: 'simplified',
      label: t('tmpl.simplified'),
      purpose: t('tmpl.simplifiedHint'),
    },
    {
      key: 'credit_note',
      label: t('comp.creditNote'),
      purpose: t('tmpl.creditHint'),
    },
    {
      key: 'debit_note',
      label: t('tmpl.debitNote'),
      purpose: t('tmpl.debitHint'),
    },
  ];
}

type Load =
  | { state: 'loading' }
  | { state: 'ready'; templates: DocumentTemplate[] }
  | { state: 'failed'; message: string };

export function TemplatePanel({ companyId }: { companyId: string }) {
  const t = useT();
  const { client, can } = useAuth();
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [active, setActive] = useState<DocType>('standard');
  const mayEdit = can('identity.edit');

  const reload = useCallback(async () => {
    setLoad({ state: 'loading' });
    try {
      setLoad({ state: 'ready', templates: await fetchTemplates(client, companyId) });
    } catch (err) {
      setLoad({ state: 'failed', message: explain(err, t) });
    }
  }, [client, companyId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  if (load.state === 'loading') {
    return (
      <div className="ds-panel tmpl">
        <div className="ds-panel__body">
          <div className="ds-skeleton" style={{ blockSize: 220 }} />
        </div>
      </div>
    );
  }

  if (load.state === 'failed') {
    return (
      <div className="ds-panel tmpl">
        <div className="ds-state">
          <p className="ds-state__title">{t('tpl.couldNotRead')}</p>
          <p className="ds-state__body">{load.message}</p>
          <button className="ds-btn ds-btn--secondary" onClick={() => void reload()}>
            {t('common.tryAgain')}
          </button>
        </div>
      </div>
    );
  }

  const current = load.templates.find((t) => t.doc_type === active);

  return (
    <div className="ds-panel tmpl">
      <div className="ds-panel__head">
        <h2 className="ds-h2">{t('tpl.documentText')}</h2>
      </div>

      <div className="ds-panel__body">
        <p className="ds-body-sm ds-muted tmpl__lede">{t('tmpl.beyondFigures')}</p>

        {/* Which type is being edited. A tab strip rather than a dropdown: four
            is few enough to show, and which ones you have customised is worth
            seeing at a glance. */}
        <div className="tmpl__tabs" role="tablist" aria-label={t('tpl.documentType')}>
          {documentTypes(t).map((type) => {
            const configured = load.templates.find(
              (x) => x.doc_type === type.key,
            )?.configured;
            return (
              <button
                key={type.key}
                role="tab"
                aria-selected={active === type.key}
                className={`tmpl__tab${active === type.key ? ' tmpl__tab--on' : ''}`}
                onClick={() => setActive(type.key)}
              >
                {type.label}
                {configured && (
                  <span className="tmpl__dot" aria-label="customised">
                    &middot;
                  </span>
                )}
              </button>
            );
          })}
        </div>

        {current && (
          <TemplateForm
            key={current.doc_type}
            companyId={companyId}
            template={current}
            purpose={documentTypes(t).find((type) => type.key === active)?.purpose ?? ''}
            readOnly={!mayEdit}
            onSaved={(saved) =>
              setLoad({
                state: 'ready',
                templates: load.templates.map((t) =>
                  t.doc_type === saved.doc_type ? saved : t,
                ),
              })
            }
            onReset={() => void reload()}
          />
        )}

        {/* The reassurance that makes the screen usable. A client who suspects
            editing a footer might alter last quarter's invoices will never
            touch it. */}
        <p className="tmpl__note" role="note">
          <strong>{t('tpl.notRetroactive')}</strong> {t('tmpl.stationery')}
        </p>
      </div>
    </div>
  );
}

function TemplateForm({
  companyId,
  template,
  purpose,
  readOnly,
  onSaved,
  onReset,
}: {
  companyId: string;
  template: DocumentTemplate;
  purpose: string;
  readOnly: boolean;
  onSaved: (saved: DocumentTemplate) => void;
  onReset: () => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const [draft, setDraft] = useState<DocumentTemplate>(template);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});

  const set = (k: keyof DocumentTemplate, v: string | boolean) => {
    setDraft((c) => ({ ...c, [k]: v }));
    setSaved(false);
  };

  async function submit() {
    setProblem(null);
    setFields({});
    setBusy(true);
    try {
      onSaved(await saveTemplate(client, companyId, draft));
      setSaved(true);
    } catch (err) {
      // Field-level messages come back keyed as the form keys them, so each
      // sits under its own box rather than as a banner the reader has to hunt.
      if (err instanceof RequestFailed && err.fields) setFields(err.fields);
      setProblem(explain(err, t));
    } finally {
      setBusy(false);
    }
  }

  async function reset() {
    setProblem(null);
    setBusy(true);
    try {
      await resetTemplate(client, companyId, draft.doc_type);
      onReset();
    } catch (err) {
      setProblem(explain(err, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="tmpl__form" aria-label={purpose}>
      <p className="ds-caption tmpl__purpose">{purpose}</p>

      <Block
        label={t('tpl.header')}
        hint={t('tmpl.subheaderHint')}
        id={`hdr-${draft.doc_type}`}
        value={draft.header_text}
        valueAr={draft.header_text_ar}
        error={fields.header_text}
        errorAr={fields.header_text_ar}
        onChange={(v) => set('header_text', v)}
        onChangeAr={(v) => set('header_text_ar', v)}
        readOnly={readOnly}
      />

      <Block
        label={t('common.paymentTerms')}
        hint={t('tmpl.paymentHint')}
        id={`pay-${draft.doc_type}`}
        value={draft.payment_terms}
        valueAr={draft.payment_terms_ar}
        error={fields.payment_terms}
        errorAr={fields.payment_terms_ar}
        onChange={(v) => set('payment_terms', v)}
        onChangeAr={(v) => set('payment_terms_ar', v)}
        readOnly={readOnly}
      />

      <Block
        label={t('tpl.returnsPolicy')}
        hint={t('tmpl.termsHint')}
        id={`ret-${draft.doc_type}`}
        value={draft.return_policy}
        valueAr={draft.return_policy_ar}
        error={fields.return_policy}
        errorAr={fields.return_policy_ar}
        onChange={(v) => set('return_policy', v)}
        onChangeAr={(v) => set('return_policy_ar', v)}
        readOnly={readOnly}
        long
      />

      <Block
        label={t('tpl.closingLine')}
        hint={t('tmpl.footerHint')}
        id={`ftr-${draft.doc_type}`}
        value={draft.footer_text}
        valueAr={draft.footer_text_ar}
        error={fields.footer_text}
        errorAr={fields.footer_text_ar}
        onChange={(v) => set('footer_text', v)}
        onChangeAr={(v) => set('footer_text_ar', v)}
        readOnly={readOnly}
      />

      <div className="tmpl__toggles">
        <label className="tmpl__check">
          <input
            type="checkbox"
            checked={draft.show_logo}
            disabled={readOnly}
            onChange={(e) => set('show_logo', e.target.checked)}
          />
          <span>
            <strong>{t('tpl.showLogo')}</strong>
            <span className="ds-caption">{t('tmpl.offRemovesMark')}</span>
          </span>
        </label>

        <label className="tmpl__check">
          <input
            type="checkbox"
            checked={draft.show_tax_number}
            disabled={readOnly}
            onChange={(e) => set('show_tax_number', e.target.checked)}
          />
          <span>
            <strong>{t('tpl.showTaxNumbers')}</strong>
            <span className="ds-caption">
              {t('tpl.taxNumberNote')}
            </span>
          </span>
        </label>
      </div>

      <FormError message={problem} />
      {saved && (
        <p className="tmpl__saved ds-body-sm" role="status">
          {t('tpl.saved')}
        </p>
      )}

      {readOnly ? (
        <p className="tmpl__readonly ds-body-sm" role="note">{t('tmpl.readOnly')}</p>
      ) : (
        <div className="tmpl__actions">
          {draft.configured && (
            <button
              className="ds-btn ds-btn--quiet"
              disabled={busy}
              onClick={() => void reset()}
            >
              {t('tpl.resetToDefault')}
            </button>
          )}
          <button
            className="ds-btn ds-btn--primary"
            disabled={busy}
            onClick={() => void submit()}
          >
            {busy ? t('action.saving') : t('action.save')}
          </button>
        </div>
      )}
    </section>
  );
}

/** One block, in both languages.
 *
 * Side by side rather than behind a language switch: a shop writing a Saudi
 * invoice needs both, and a switch hides the half somebody forgot to fill in. */
function Block({
  label,
  hint,
  id,
  value,
  valueAr,
  error,
  errorAr,
  onChange,
  onChangeAr,
  readOnly,
  long,
}: {
  label: string;
  hint: string;
  id: string;
  value: string;
  valueAr: string;
  error?: string;
  errorAr?: string;
  onChange: (v: string) => void;
  onChangeAr: (v: string) => void;
  readOnly: boolean;
  long?: boolean;
}) {
  const t = useT();
  return (
    <div className="tmpl__block">
      <p className="tmpl__blocklabel">
        {label}
        <span className="ds-caption">{hint}</span>
      </p>
      <div className="tmpl__pair">
        <Field label={t('tpl.english')} htmlFor={`${id}-en`} error={error}>
          {long ? (
            <textarea
              id={`${id}-en`}
              className="field__input tmpl__area"
              rows={4}
              value={value}
              readOnly={readOnly}
              onChange={(e) => onChange(e.target.value)}
            />
          ) : (
            <TextInput
              id={`${id}-en`}
              value={value}
              onChange={onChange}
              error={error}
            />
          )}
        </Field>

        <Field label="العربية" htmlFor={`${id}-ar`} error={errorAr}>
          {long ? (
            <textarea
              id={`${id}-ar`}
              className="field__input tmpl__area"
              rows={4}
              lang="ar"
              dir="rtl"
              value={valueAr}
              readOnly={readOnly}
              onChange={(e) => onChangeAr(e.target.value)}
            />
          ) : (
            <input
              id={`${id}-ar`}
              className="field__input"
              lang="ar"
              dir="rtl"
              value={valueAr}
              readOnly={readOnly}
              onChange={(e) => onChangeAr(e.target.value)}
            />
          )}
        </Field>
      </div>
    </div>
  );
}

function explain(err: unknown, t: (key: Key) => string): string {
  if (err instanceof Offline) {
    return (
      t('tmpl.offlineOnServer') +
      'until the connection is back. Nothing already saved is lost.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return t('tmpl.notAllowed');
    return err.message;
  }
  return err instanceof Error ? err.message : t('common.somethingWrong');
}

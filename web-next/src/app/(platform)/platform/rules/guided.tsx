'use client';

// Recording a legal value by typing the figures, not by composing JSON.
//
// # What this replaces
//
// A rule seeded with `__VERIFY__` blocks a market until somebody records it,
// and the only way to record one was a textarea taking a JSON object. An
// operator had to work out from the placeholder keys alone that
// `resignation_fraction_two_to_five_years` was a fraction between nought and
// one, from Article 85, of the award Article 84 defines — and then hand-write
// the braces.
//
// The product knows all of that. `GET /platform/rules/sources` says, for each
// unrecorded value, which document to read, which articles, what each field is
// called, what unit it is in and what it means. This renders that as a form.
//
// # What it deliberately does not do
//
// It does not supply the figures. There is no machine-readable authoritative
// source for them — the Labour Law is published as prose — so a default here
// would be a number somebody typed from memory, shown to every deployment as
// though the product had established it. The registry's whole discipline is
// that a reader can tell a confirmed figure from a guess.
//
// # Confirming is separate from recording
//
// The tick is `verified`, and it is off by default. Recording without it keeps
// the figures and asserts nothing, which is how one person stages a value for
// another to confirm; nothing uses it until somebody does. Ticking it puts
// their name against having read the document.

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';

export interface SourceField {
  name: string;
  label: string;
  kind: 'decimal' | 'int' | 'text' | 'choice';
  choices?: string[];
  unit?: string;
  article?: string;
  help: string;
}

export interface SourceRule {
  rule_key: string;
  country: string;
  title: string;
  authority: string;
  document: string;
  url: string;
  articles?: string[];
  reading: string;
  fields: SourceField[];
}

export function GuidedRuleForm({
  source,
  blocker,
  onRecorded,
  onCancel,
  onAdvanced,
}: {
  source: SourceRule;
  /** Carried through so recording does not silently stop a rule blocking. */
  blocker: boolean;
  onRecorded: () => void;
  onCancel: () => void;
  onAdvanced: () => void;
}) {
  const t = useT();

  const [values, setValues] = useState<Record<string, string>>({});
  const [from, setFrom] = useState('');
  const [verified, setVerified] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const missing = source.fields.filter((f) => (values[f.name] ?? '').trim() === '');
  const ready = missing.length === 0 && from.trim() !== '';

  async function record() {
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      // Every value is sent as a STRING, which is what the registry stores and
      // what `Decimal()` parses. A number here would go through JavaScript's
      // float on the way, and a rate that is wrong in the last place is wrong
      // on a payslip.
      const payload: Record<string, string> = {};
      for (const f of source.fields) payload[f.name] = (values[f.name] ?? '').trim();

      await api.post('/platform/rules', {
        rule_key: source.rule_key,
        country: source.country,
        payload,
        effective_from: from,
        source_authority: source.authority,
        source_document: source.document,
        source_url: source.url,
        release_blocker: blocker,
        verified,
        // A value recorded without confirmation must explain itself, and the
        // service refuses it otherwise. Saying who it is waiting for is more
        // use than "staged".
        notes: verified
          ? ''
          : `Figures recorded from ${source.document} and awaiting confirmation by a second reader.`,
      });
      onRecorded();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rounded-md border border-line bg-surface p-4">
      <h3 className="text-card-title font-semibold text-fg">
        {t('nx.plat.ruGuidedTitle', { title: source.title })}
      </h3>

      <p className="mt-1 max-w-prose text-body text-muted">{source.reading}</p>

      <p className="mt-2 text-caption text-muted">
        {t('nx.plat.ruGuidedRead', { document: source.document })}
        {source.articles && source.articles.length > 0
          ? ` · ${t('nx.plat.ruGuidedArticles', { articles: source.articles.join(', ') })}`
          : ''}
        {source.url ? (
          <>
            {' · '}
            <a
              href={source.url}
              target="_blank"
              rel="noreferrer"
              className="text-primary underline"
            >
              {t('nx.plat.ruGuidedOpen')}
            </a>
          </>
        ) : null}
      </p>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        {source.fields.map((f) => (
          <Field
            key={f.name}
            name={f.name}
            label={f.article ? `${f.label} · ${f.article}` : f.label}
            hint={f.unit ? `${f.help} (${f.unit})` : f.help}
            error={fieldErrors?.[f.name] ?? fieldErrors?.payload}
          >
            {f.kind === 'choice' ? (
              <Select
                value={values[f.name] ?? ''}
                onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
              >
                <option value="">{t('nx.plat.ruGuidedChoose')}</option>
                {(f.choices ?? []).map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </Select>
            ) : (
              <Input
                // Latin and left-to-right even in Arabic: these are figures a
                // reader compares against a printed law, and a mirrored
                // fraction is a different number.
                dir="ltr"
                className="num"
                inputMode={f.kind === 'text' ? undefined : 'decimal'}
                value={values[f.name] ?? ''}
                onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
              />
            )}
          </Field>
        ))}

        <Field
          name="effective_from"
          label={t('nx.plat.ruGuidedEffective')}
          hint={t('nx.plat.ruGuidedEffectiveHint')}
          error={fieldErrors?.effective_from}
        >
          <Input
            type="date"
            dir="ltr"
            className="num"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
        </Field>
      </div>

      <div className="mt-4">
        <Checkbox
          label={t('nx.plat.ruGuidedConfirm')}
          hint={t('nx.plat.ruGuidedConfirmHint')}
          checked={verified}
          onChange={(e) => setVerified(e.target.checked)}
        />
      </div>

      {error ? <FormError message={error} /> : null}
      {!ready && missing.length > 0 ? (
        <p className="mt-3 text-caption text-muted">{t('nx.plat.ruGuidedMissing')}</p>
      ) : null}

      <div className="mt-4 flex flex-wrap gap-2">
        <Button
          variant="primary"
          disabled={busy || !ready}
          onClick={() => void record()}
        >
          {t('nx.plat.ruGuidedSave')}
        </Button>
        <Button variant="ghost" disabled={busy} onClick={onCancel}>
          {t('nx.plat.ruGuidedCancel')}
        </Button>
        {/* The raw payload stays reachable. A rule this pack does not describe,
            or one whose shape changed before the pack caught up, still has to
            be recordable. */}
        <Button variant="link" disabled={busy} onClick={onAdvanced}>
          {t('nx.plat.ruGuidedAdvanced')}
        </Button>
      </div>
    </div>
  );
}

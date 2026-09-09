'use client';

// Putting a tax authority on file, and putting a rate on it.
//
// # Why this had to be built
//
// E8's own note says it plainly: every write path into the registry existed and
// "none of it was reachable... the operation could only be performed with a SQL
// client against production". The rules half was answered by the regulatory
// screen. The tax half was not: `POST /platform/jurisdictions`,
// `POST /platform/jurisdictions/{id}/rates` and
// `POST /platform/jurisdictions/import` had no caller anywhere in the front
// end, so the only Californian rates this product has are the ones a migration
// loaded, and no operator could add a county, correct a rate, or bring in next
// quarter's schedule.
//
// # A schedule is pasted, not uploaded
//
// CDTFA publishes a spreadsheet, not an API. Somebody opens it, copies the
// columns and pastes them here; the product's job is to apply the result in one
// transaction with the source attached, which is exactly what the import route
// does. A file picker would be a nicer-looking way to do the same thing and
// would also mean parsing whatever spreadsheet dialect arrived, silently, on a
// screen where a misread column is a wrong rate charged to every customer in a
// county.
//
// So: tab- or comma-separated, one row per authority, columns named in the
// heading above the box. Rows that do not parse are reported by line number
// before anything is sent, because a schedule that lands half-applied is worse
// than one that does not land.
//
// # Nothing here marks itself verified
//
// `verified` is deliberately not sent. Recording a rate is one act and standing
// behind it is another, and the second happens on the rates screen where two
// different people are required. An import that could tick its own box would
// make that control decorative.

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useT } from '@/lib/i18n/locale';
import { parseSchedule } from '@/lib/tax/schedule';

// The levels the jurisdiction tree understands, and nothing else.
//
// `tax_jurisdiction_level_known` is a CHECK constraint, so a level this list
// invented would be refused by the database with its own constraint name and
// no sentence anybody could act on. Driving the dropdown from anything wider
// than the constraint is how a form collects a 400 it could have prevented.
const LEVELS = ['country', 'state', 'county', 'city', 'district'] as const;

/** How a sale is treated for tax. The registry's own vocabulary. */
const TREATMENTS = ['taxable', 'zero_rated', 'exempt', 'out_of_scope'] as const;

export function JurisdictionAdmin({
  country,
  jurisdictions,
  onSaved,
}: {
  country: string;
  jurisdictions: readonly { id: string; code: string; name: string }[];
  onSaved: () => void;
}) {
  const t = useT();

  const [open, setOpen] = useState<'authority' | 'rate' | 'import' | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState<string | null>(null);

  // A tax authority.
  const [level, setLevel] = useState<string>('state');
  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [parentID, setParentID] = useState('');
  const [originBased, setOriginBased] = useState('');

  // One rate on one authority.
  const [rateJurisdiction, setRateJurisdiction] = useState('');
  const [treatment, setTreatment] = useState<string>('taxable');
  const [rate, setRate] = useState('');
  const [from, setFrom] = useState('');

  // The provenance every write here needs. Shared between the rate form and
  // the import, because they are the same three questions.
  const [authority, setAuthority] = useState('');
  const [document, setDocument] = useState('');
  const [url, setUrl] = useState('');

  // A pasted schedule.
  const [schedule, setSchedule] = useState('');
  const parsed = parseSchedule(schedule);

  function begin(which: 'authority' | 'rate' | 'import') {
    setOpen((v) => (v === which ? null : which));
    setError(null);
    setFieldErrors({});
    setDone(null);
  }

  async function run(fn: () => Promise<string>) {
    setBusy(true);
    setError(null);
    setFieldErrors({});
    setDone(null);
    try {
      const said = await fn();
      setDone(said);
      setOpen(null);
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const provenance = (
    <>
      <Field
        name="source_authority"
        label={t('nx.plat.juAuthority')}
        hint={t('nx.plat.juAuthorityHint')}
        error={fieldErrors.source_authority}
      >
        <Input value={authority} onChange={(e) => setAuthority(e.target.value)} />
      </Field>
      <Field
        name="source_document"
        label={t('nx.plat.juDocument')}
        hint={t('nx.plat.juDocumentHint')}
        error={fieldErrors.source_document}
        required
      >
        <Input value={document} onChange={(e) => setDocument(e.target.value)} />
      </Field>
      <Field
        name="source_url"
        label={t('nx.plat.juUrl')}
        hint={t('nx.plat.juUrlHint')}
        error={fieldErrors.source_url}
      >
        <Input dir="ltr" value={url} onChange={(e) => setUrl(e.target.value)} />
      </Field>
    </>
  );

  return (
    <Panel
      className="mb-4"
      title={t('nx.plat.juAdminTitle')}
      description={t('nx.plat.juAdminHint')}
      actions={
        <>
          <Button
            size="sm"
            variant={open === 'authority' ? 'primary' : 'ghost'}
            onClick={() => begin('authority')}
          >
            {t('nx.plat.juAddAuthority')}
          </Button>
          <Button
            size="sm"
            variant={open === 'rate' ? 'primary' : 'ghost'}
            onClick={() => begin('rate')}
          >
            {t('nx.plat.juRecordRate')}
          </Button>
          <Button
            size="sm"
            variant={open === 'import' ? 'primary' : 'ghost'}
            onClick={() => begin('import')}
          >
            {t('nx.plat.juImport')}
          </Button>
        </>
      }
    >
      <FormError message={error} fields={fieldErrors} className="mb-4" />
      {done ? <p className="mb-4 text-body text-positive-fg">{done}</p> : null}

      {open === null ? (
        <p className="max-w-prose text-body text-muted">{t('nx.plat.juAdminIdle')}</p>
      ) : null}

      {open === 'authority' ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field name="level" label={t('nx.plat.juLevel')} error={fieldErrors.level}>
            <Select value={level} onChange={(e) => setLevel(e.target.value)}>
              {LEVELS.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </Select>
          </Field>
          <Field
            name="code"
            label={t('nx.plat.juCode')}
            hint={t('nx.plat.juCodeHint')}
            error={fieldErrors.code}
            required
          >
            <Input
              className="num"
              value={code}
              onChange={(e) => setCode(e.target.value.toUpperCase())}
            />
          </Field>
          <Field
            name="name"
            label={t('nx.plat.juName')}
            error={fieldErrors.name}
            required
          >
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          {/* A country IS the root of its own tree and everything else hangs
              off something: `tax_jurisdiction_root_is_country` makes the two
              cases exclusive. So the field appears only where it applies, and
              is required where it does. Without that, a city could be
              parentless and its state's share would silently never apply. */}
          {level === 'country' ? (
            <div>
              <p className="text-label text-muted">{t('nx.plat.juParent')}</p>
              <p className="mt-0.5 text-body text-fg">{t('nx.plat.juNoParent')}</p>
              <p className="mt-1 max-w-prose text-caption text-muted">
                {t('nx.plat.juCountryIsRoot')}
              </p>
            </div>
          ) : (
            <Field
              name="parent_id"
              label={t('nx.plat.juParent')}
              hint={t('nx.plat.juParentHint')}
              error={fieldErrors.parent_id}
              required
            >
              <Select value={parentID} onChange={(e) => setParentID(e.target.value)}>
                <option value="">{t('nx.plat.juChooseParent')}</option>
                {jurisdictions.map((j) => (
                  <option key={j.id} value={j.id}>
                    {j.name} ({j.code})
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field
            name="is_origin_based"
            label={t('nx.plat.juBasis')}
            hint={t('nx.plat.juBasisHint')}
            error={fieldErrors.is_origin_based}
          >
            <Select
              value={originBased}
              onChange={(e) => setOriginBased(e.target.value)}
            >
              <option value="">{t('nx.plat.juBasisUnsaid')}</option>
              <option value="true">{t('nx.plat.juOrigin')}</option>
              <option value="false">{t('nx.plat.juDestination')}</option>
            </Select>
          </Field>
          <div className="flex items-end">
            <Button
              variant="primary"
              disabled={
                busy ||
                !code.trim() ||
                !name.trim() ||
                (level !== 'country' && parentID === '')
              }
              onClick={() =>
                void run(async () => {
                  await api.post('/platform/jurisdictions', {
                    country,
                    level,
                    code: code.trim(),
                    name: name.trim(),
                    parent_id: parentID,
                    // Left unsaid stays unsaid. Sending `false` would assert
                    // destination-based sourcing on an authority nobody has
                    // looked up, and that decides which rate a sale is taxed at.
                    is_origin_based:
                      originBased === '' ? undefined : originBased === 'true',
                  });
                  setCode('');
                  setName('');
                  return t('nx.plat.juAuthoritySaved');
                })
              }
            >
              {busy ? t('nx.plat.opSaving') : t('nx.plat.juSaveAuthority')}
            </Button>
          </div>
        </div>
      ) : null}

      {open === 'rate' ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field
            name="jurisdiction_id"
            label={t('nx.plat.juAuthorityPick')}
            error={fieldErrors.jurisdiction_id}
            required
          >
            <Select
              value={rateJurisdiction}
              onChange={(e) => setRateJurisdiction(e.target.value)}
            >
              <option value="">{t('nx.plat.juChooseAuthority')}</option>
              {jurisdictions.map((j) => (
                <option key={j.id} value={j.id}>
                  {j.name} ({j.code})
                </option>
              ))}
            </Select>
          </Field>
          <Field
            name="treatment"
            label={t('nx.plat.juTreatment')}
            error={fieldErrors.treatment}
          >
            <Select value={treatment} onChange={(e) => setTreatment(e.target.value)}>
              {TREATMENTS.map((x) => (
                <option key={x} value={x}>
                  {x}
                </option>
              ))}
            </Select>
          </Field>
          <Field
            name="rate"
            label={t('nx.plat.juRate')}
            hint={t('nx.plat.juRateHint')}
            error={fieldErrors.rate}
            required
          >
            <Input
              className="num"
              inputMode="decimal"
              value={rate}
              onChange={(e) => setRate(e.target.value)}
            />
          </Field>
          <Field
            name="effective_from"
            label={t('nx.plat.juFrom')}
            hint={t('nx.plat.juFromHint')}
            error={fieldErrors.effective_from}
            required
          >
            <Input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
            />
          </Field>
          {provenance}
          <div className="flex items-end">
            <Button
              variant="primary"
              disabled={
                busy || !rateJurisdiction || !rate.trim() || !from || !document.trim()
              }
              onClick={() =>
                void run(async () => {
                  await api.post(
                    `/platform/jurisdictions/${rateJurisdiction}/rates`,
                    {
                      treatment,
                      rate: rate.trim(),
                      effective_from: from,
                      source_authority: authority.trim(),
                      source_document: document.trim(),
                      source_url: url.trim(),
                    },
                  );
                  setRate('');
                  return t('nx.plat.juRateSaved');
                })
              }
            >
              {busy ? t('nx.plat.opSaving') : t('nx.plat.juSaveRate')}
            </Button>
          </div>
        </div>
      ) : null}

      {open === 'import' ? (
        <div className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Field
              name="treatment"
              label={t('nx.plat.juTreatment')}
              error={fieldErrors.treatment}
            >
              <Select value={treatment} onChange={(e) => setTreatment(e.target.value)}>
                {TREATMENTS.map((x) => (
                  <option key={x} value={x}>
                    {x}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="effective_from"
              label={t('nx.plat.juFrom')}
              hint={t('nx.plat.juFromHint')}
              error={fieldErrors.effective_from}
              required
            >
              <Input
                type="date"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
            </Field>
            {provenance}
          </div>

          <Field
            name="rows"
            label={t('nx.plat.juSchedule')}
            hint={t('nx.plat.juScheduleHint')}
            error={fieldErrors.rows}
            required
          >
            <Textarea
              rows={10}
              dir="ltr"
              className="num"
              value={schedule}
              onChange={(e) => setSchedule(e.target.value)}
              placeholder={'county\tSD\tSan Diego\tCA\t0.0775'}
            />
          </Field>

          {/* What was understood, before anything is sent. A schedule that
              lands half-applied is worse than one that does not land. */}
          <p className="text-body text-muted">
            {t('nx.plat.juParsed', { n: String(parsed.rows.length) })}
            {parsed.bad.length > 0 ? (
              <span className="ms-2 text-critical-fg">
                {t('nx.plat.juUnreadable', { lines: parsed.bad.join(', ') })}
              </span>
            ) : null}
          </p>

          <div>
            <Button
              variant="primary"
              disabled={
                busy ||
                parsed.rows.length === 0 ||
                parsed.bad.length > 0 ||
                !from ||
                !document.trim()
              }
              onClick={() =>
                void run(async () => {
                  const out = await api.post<{ result: { imported?: number } }>(
                    '/platform/jurisdictions/import',
                    {
                      country,
                      treatment,
                      effective_from: from,
                      source_authority: authority.trim(),
                      source_document: document.trim(),
                      source_url: url.trim(),
                      rows: parsed.rows,
                    },
                  );
                  setSchedule('');
                  return t('nx.plat.juImported', {
                    n: String(out.result?.imported ?? parsed.rows.length),
                  });
                })
              }
            >
              {busy ? t('nx.plat.opSaving') : t('nx.plat.juRunImport')}
            </Button>
          </div>
        </div>
      ) : null}
    </Panel>
  );
}

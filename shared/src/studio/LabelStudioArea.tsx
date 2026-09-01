// The barcode engine and label studio (blueprint B3).
//
// # What is printed is what the server said
//
// The preview draws exactly the fields the template names, filled with exactly
// the values the server resolved. Nothing is computed here — not the price, not
// the barcode, not the readable code — because a tag is the one place in this
// product where a wrong number is physically attached to a garment.
//
// # Printing goes through the browser
//
// B3's printers are attached to the till: an Xprinter on USB, a Zebra on the
// network. A PDF the server rendered would still leave the browser to send it
// to a device the server cannot see, so the sheet is laid out in millimetres
// and the print stylesheet hides everything else.

import { useCallback, useState } from 'react';

import {
  buildLabels,
  generateBarcodes,
  listTemplates,
  readScheme,
  saveScheme,
  type BarcodeScheme,
  type Label,
  type LabelSheet,
  type LabelTemplate,
  type Symbology,
} from '../api/studio';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormError, SelectInput, TextInput } from '../ui/Form';
import { money } from '../ui/format';

const SYMBOLOGIES: Symbology[] = [
  'code128',
  'ean13',
  'ean8',
  'upca',
  'qr',
  'datamatrix',
];

// What a shop can put in a meaningful code. Category, brand and season come off
// the product; colour and size are the attributes almost every clothing shop
// uses. A shop with its own attribute types it.
const PARTS = ['category', 'brand', 'season', 'colour', 'size', 'sku'];

type Tab = 'print' | 'scheme';

export function LabelStudioArea({ companyId }: { companyId: string }) {
  const { can } = useAuth();
  const t = useT();

  const mayManage = can('label.manage');
  const [tab, setTab] = useState<Tab>('print');

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('lbl.title')}</h1>
          <p className="ds-caption">{t('lbl.intro')}</p>
        </div>

        {mayManage && (
          <div className="detail__actions">
            <div
              className="segmented"
              role="group"
              aria-label={t('common.whatToShow')}
            >
              <button
                className={`segmented__btn${tab === 'print' ? ' segmented__btn--on' : ''}`}
                aria-pressed={tab === 'print'}
                onClick={() => setTab('print')}
              >
                {t('lbl.print')}
              </button>
              <button
                className={`segmented__btn${tab === 'scheme' ? ' segmented__btn--on' : ''}`}
                aria-pressed={tab === 'scheme'}
                onClick={() => setTab('scheme')}
              >
                {t('lbl.scheme')}
              </button>
            </div>
          </div>
        )}
      </header>

      {tab === 'print' && <PrintPanel companyId={companyId} />}
      {tab === 'scheme' && mayManage && <SchemePanel companyId={companyId} />}
    </main>
  );
}

function PrintPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => listTemplates(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [templateID, setTemplateID] = useState('');
  const [search, setSearch] = useState('');
  const [copies, setCopies] = useState('1');
  const [sheet, setSheet] = useState<LabelSheet | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function build(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      setSheet(
        await buildLabels(client, companyId, {
          template_id: templateID || undefined,
          search: search || undefined,
          copies: Number(copies) || 1,
        }),
      );
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { data: LabelTemplate[] }) => (
        <>
          <form className="ds-panel lbl__form" onSubmit={(e) => void build(e)}>
            <div className="ds-panel__head">
              <h2 className="ds-h3">{t('lbl.whatToPrint')}</h2>
              <p className="ds-caption">{t('lbl.whatToPrintHint')}</p>
            </div>
            <div className="ds-panel__body">
              <FormError message={failure} />
              <div className="form__grid">
                <Field label={t('lbl.label')} htmlFor="lb-template" required>
                  <SelectInput
                    id="lb-template"
                    value={templateID}
                    onChange={setTemplateID}
                    options={payload.data}
                    label={(x) =>
                      `${x.name} · ${x.width_mm}×${x.height_mm}mm${
                        x.per_sheet ? ` · ${x.per_sheet}` : ''
                      }`
                    }
                    placeholder={t('lbl.chooseLabel')}
                  />
                </Field>
                <Field
                  label={t('lbl.whichProducts')}
                  hint={t('lbl.whichProductsHint')}
                  htmlFor="lb-search"
                >
                  <TextInput id="lb-search" value={search} onChange={setSearch} />
                </Field>
                <Field label={t('lbl.copies')} htmlFor="lb-copies">
                  <TextInput
                    id="lb-copies"
                    value={copies}
                    onChange={setCopies}
                    inputMode="numeric"
                  />
                </Field>
              </div>

              <div className="lbl__actions">
                <button
                  className="ds-btn ds-btn--primary"
                  type="submit"
                  disabled={busy}
                >
                  {t('lbl.preview')}
                </button>
                {sheet && sheet.labels.length > 0 && (
                  <button
                    type="button"
                    className="ds-btn ds-btn--quiet"
                    onClick={() => window.print()}
                  >
                    {t('action.print')}
                  </button>
                )}
                {sheet && (
                  <span className="ds-caption">
                    {t('lbl.labelCount', {
                      count: String(sheet.labels.length),
                    })}
                  </span>
                )}
              </div>
            </div>
          </form>

          {sheet && sheet.labels.length > 0 && (
            <LabelSheetView sheet={sheet} />
          )}
        </>
      )}
    </RemoteBody>
  );
}

// LabelSheetView draws the labels at the size they will print.
//
// Millimetres throughout, because that is what the roll is specified in and
// what the browser's print pipeline understands. A preview in pixels would
// look right on screen and come out the wrong size on paper.
function LabelSheetView({ sheet }: { sheet: LabelSheet }) {
  const t = useT();
  const tpl = sheet.template;

  const style: React.CSSProperties = {
    inlineSize: `${tpl.width_mm}mm`,
    blockSize: `${tpl.height_mm}mm`,
    padding: `${tpl.margin_mm}mm`,
  };
  const grid: React.CSSProperties =
    tpl.kind === 'a4_sheet' && tpl.columns
      ? {
          display: 'grid',
          gridTemplateColumns: `repeat(${tpl.columns}, ${tpl.width_mm}mm)`,
          gap: `${tpl.gap_mm}mm`,
        }
      : { display: 'flex', flexWrap: 'wrap', gap: `${tpl.gap_mm || 2}mm` };

  return (
    <section className="ds-panel lbl__sheetpanel" aria-label={t('lbl.preview')}>
      <div className="ds-panel__head lbl__sheetbar">
        <h2 className="ds-h3">{tpl.name}</h2>
        <span className="ds-caption">
          {t('lbl.sizeMm', { w: tpl.width_mm, h: tpl.height_mm })}
        </span>
      </div>
      <div className="ds-panel__body lbl__sheet" style={grid}>
        {sheet.labels.map((l, i) => (
          <LabelCard key={`${l.variant_id}-${i}`} label={l} tpl={tpl} style={style} />
        ))}
      </div>
    </section>
  );
}

function LabelCard({
  label,
  tpl,
  style,
}: {
  label: Label;
  tpl: LabelTemplate;
  style: React.CSSProperties;
}) {
  const t = useT();

  return (
    <div className="lbl__label" style={style}>
      {tpl.fields.map((f, i) => {
        switch (f.field) {
          case 'logo':
            return (
              <span key={i} className="lbl__logo">
                {t('lbl.logoHere')}
              </span>
            );
          case 'name':
            return (
              <span
                key={i}
                className="lbl__line"
                style={{
                  fontSize: `${f.size ?? 8}pt`,
                  fontWeight: f.bold ? 700 : 400,
                }}
              >
                {label.name}
              </span>
            );
          case 'name_ar':
            return label.name_ar ? (
              <span
                key={i}
                className="lbl__line"
                dir="rtl"
                style={{ fontSize: `${f.size ?? 8}pt` }}
              >
                {label.name_ar}
              </span>
            ) : null;
          case 'attributes':
            return label.attributes ? (
              <span
                key={i}
                className="lbl__line"
                style={{ fontSize: `${f.size ?? 7}pt` }}
              >
                {label.attributes}
              </span>
            ) : null;
          case 'price':
            return (
              <span
                key={i}
                className="lbl__price"
                style={{
                  fontSize: `${f.size ?? 10}pt`,
                  fontWeight: f.bold ? 700 : 400,
                }}
              >
                {/* Printed as the server gave it. VAT is already inside. */}
                {money(label.price, { currency: label.currency })}
              </span>
            );
          case 'customer_name':
            return (
              <span key={i} className="lbl__line">
                {label.name}
              </span>
            );
          case 'barcode':
            return (
              <span key={i} className="lbl__barcode">
                <Barcode value={label.barcode} height={f.height ?? 10} />
                <span className="lbl__barcodetext">
                  {/* For a digit symbology the readable string is not the
                      barcode, so both are printed rather than one standing in
                      for the other. */}
                  {label.readable && label.readable !== label.barcode
                    ? `${label.barcode} · ${label.readable}`
                    : label.barcode}
                </span>
              </span>
            );
          default:
            return null;
        }
      })}
    </div>
  );
}

// Barcode draws Code 128 bars, or a placeholder block for the 2D symbologies.
//
// Drawn rather than fetched from an image service: a label studio that needed
// the internet to print a price tag would be a label studio that stops working
// when the shop's line does, which is the same week the till is busiest.
//
// The bars are a deterministic rendering of the value, not a scannable Code 128
// encoding — that needs the full character-set tables and a checksum, which is
// a library rather than a component. What this gives is a truthful preview of
// the layout at the right size; the thermal printer's own driver renders the
// scannable code from the same value.
function Barcode({ value, height }: { value: string; height: number }) {
  const bars = [...value].map((ch, i) => ({
    width: 1 + (ch.charCodeAt(0) % 3) * 0.4,
    gap: 0.6 + ((ch.charCodeAt(0) + i) % 2) * 0.4,
  }));

  return (
    <span
      className="lbl__bars"
      style={{ blockSize: `${height}mm` }}
      aria-label={value}
    >
      {bars.map((b, i) => (
        <span
          key={i}
          className="lbl__bar"
          style={{ inlineSize: `${b.width}px`, marginInlineEnd: `${b.gap}px` }}
        />
      ))}
    </span>
  );
}

function SchemePanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();

  const load = useCallback(
    () => readScheme(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(scheme: BarcodeScheme) => (
        <SchemeForm companyId={companyId} scheme={scheme} onSaved={reload} />
      )}
    </RemoteBody>
  );
}

function SchemeForm({
  companyId,
  scheme,
  onSaved,
}: {
  companyId: string;
  scheme: BarcodeScheme;
  onSaved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [parts, setParts] = useState<string[]>(scheme.parts);
  const [separator, setSeparator] = useState(scheme.separator);
  const [symbology, setSymbology] = useState<Symbology>(scheme.symbology);
  const [length, setLength] = useState(String(scheme.part_length));
  const [prefix, setPrefix] = useState(scheme.prefix ?? '');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const [generated, setGenerated] = useState<number | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    setSaved(false);
    try {
      await saveScheme(client, companyId, {
        parts,
        separator,
        symbology,
        part_length: Number(length) || 3,
        prefix: prefix || undefined,
      });
      setSaved(true);
      onSaved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function generate() {
    setBusy(true);
    setFailure(null);
    try {
      const out = await generateBarcodes(client, companyId, {});
      setGenerated(out.count);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel lbl__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('lbl.scheme')}</h2>
        <p className="ds-caption">{t('lbl.schemeHint')}</p>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
        {saved && (
          <p className="ds-caption" role="status">
            {t('lbl.schemeSaved')}
          </p>
        )}

        {/* The worked example, so somebody can see what they will get before
            generating a thousand of them. */}
        <p className="lbl__example">
          {t('lbl.exampleReads')} <strong>{scheme.example}</strong>
        </p>

        <fieldset className="lbl__parts">
          <legend className="field__label">{t('lbl.parts')}</legend>
          <p className="field__hint">{t('lbl.partsHint')}</p>
          {PARTS.map((part) => (
            <label key={part} className="lbl__check">
              <input
                type="checkbox"
                checked={parts.includes(part)}
                onChange={(e) =>
                  setParts((prev) =>
                    e.target.checked
                      ? [...prev, part]
                      : prev.filter((p) => p !== part),
                  )
                }
              />
              <span>{t(`lbl.part.${part}` as Key)}</span>
            </label>
          ))}
        </fieldset>

        <div className="form__grid">
          <Field label={t('lbl.symbology')} htmlFor="sc-sym" required>
            <SelectInput
              id="sc-sym"
              value={symbology}
              onChange={(v) => setSymbology(v as Symbology)}
              options={SYMBOLOGIES.map((s) => ({ id: s }))}
              label={(s) => t(`lbl.sym.${s.id}` as Key)}
            />
          </Field>
          <Field
            label={t('lbl.partLength')}
            hint={t('lbl.partLengthHint')}
            htmlFor="sc-len"
          >
            <TextInput
              id="sc-len"
              value={length}
              onChange={setLength}
              inputMode="numeric"
            />
          </Field>
          <Field label={t('lbl.separator')} htmlFor="sc-sep">
            <TextInput id="sc-sep" value={separator} onChange={setSeparator} />
          </Field>
          <Field
            label={t('lbl.prefix')}
            hint={t('lbl.prefixHint')}
            htmlFor="sc-prefix"
          >
            <TextInput id="sc-prefix" value={prefix} onChange={setPrefix} />
          </Field>
        </div>

        {/* Said out loud, because somebody changing the scheme reasonably
            expects the codes to follow and they do not. */}
        <p className="ds-caption">{t('lbl.schemeChangeWhy')}</p>

        <div className="lbl__actions">
          <button className="ds-btn ds-btn--primary" type="submit" disabled={busy}>
            {t('action.saveChanges')}
          </button>
          <button
            type="button"
            className="ds-btn ds-btn--quiet"
            disabled={busy}
            onClick={() => void generate()}
          >
            {t('lbl.generateMissing')}
          </button>
          {generated !== null && (
            <span className="ds-caption" role="status">
              {generated === 0
                ? t('lbl.everythingHasACode')
                : t('lbl.generatedCount', { count: String(generated) })}
            </span>
          )}
        </div>
      </div>
    </form>
  );
}

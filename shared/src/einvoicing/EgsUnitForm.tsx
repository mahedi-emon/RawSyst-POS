// Creating an e-invoicing unit, and correcting one.
//
// The form is in two parts, and the split is the point. The top part is what a
// shop must decide — a name, how signing works, which branch — and it is short
// because those are the only choices that cannot be put off. The bottom part is
// the nine fields ZATCA asks for when the unit is registered, and every one of
// them is optional here, because a till that cannot be set up until somebody
// has found the business's industry classification is a till that does not
// trade today.
//
// # The architecture is chosen once
//
// It decides where the private signing key lives (Technical Guideline V2 §3.5),
// and moving that under a chain that already exists is not a correction. So it
// is offered on creation and shown as a fact afterwards.
//
// # Nothing here claims a certificate
//
// There is no field for a CSID, because the server has no route that accepts
// one. The unit's certification state is reported, never entered.

import { useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import {
  amendEgsUnit,
  createEgsUnit,
  emptyCsr,
  type Architecture,
  type Csr,
  type EgsUnit,
} from '../api/egs';
import type { DeviceStore } from '../api/devices';
import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
  type FieldErrors,
} from '../ui/Form';
import {
  architectures,
  architectureName,
  invoiceTypes,
  organizationUnitProblem,
  vatNumberProblem,
} from './egs';

export function EgsUnitForm({
  companyId,
  stores,
  existing,
  onSaved,
  onCancel,
}: {
  companyId: string;
  stores: DeviceStore[];
  /** The unit being corrected. Absent when creating one. */
  existing?: EgsUnit;
  onSaved: (unit: EgsUnit) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [label, setLabel] = useState(existing?.label ?? '');
  const [architecture, setArchitecture] = useState<Architecture>(
    existing?.architecture ?? 'smart_pos',
  );
  const [storeId, setStoreId] = useState(
    existing?.store_id ?? (stores.length === 1 ? stores[0]!.id : ''),
  );
  const [csr, setCsr] = useState<Csr>(existing?.csr ?? emptyCsr);

  const [fields, setFields] = useState<FieldErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const central = architecture === 'centralized_server';
  const set = (key: keyof Csr) => (v: string) => setCsr((c) => ({ ...c, [key]: v }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();

    // Cheap checks to save a round trip, never the only ones: the same rules
    // run in Go and, for the two formats, in the database.
    const local: FieldErrors = {};
    if (!label.trim()) local.label = 'Give this unit a name, like "Main branch".';
    if (!central && !storeId) local.store_id = t('egs.chooseBranch');

    const vat = vatNumberProblem(csr.organization_identifier);
    if (vat) local['csr.organization_identifier'] = vat;
    const member = organizationUnitProblem(csr);
    if (member) local['csr.organization_unit'] = member;

    if (Object.keys(local).length > 0) {
      setFields(local);
      setFailure(null);
      return;
    }

    setBusy(true);
    setFields({});
    setFailure(null);
    try {
      const body = { label: label.trim(), store_id: central ? undefined : storeId, csr };
      onSaved(
        existing
          ? await amendEgsUnit(client, companyId, existing.id, body)
          : await createEgsUnit(client, companyId, { ...body, architecture }),
      );
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          t('egs.saveOffline') +
            t('common.tryWhenBack'),
        );
      } else if (err instanceof RequestFailed) {
        if (err.fields) setFields(err.fields);
        setFailure(err.fields ? null : err.message);
      } else {
        setFailure(err instanceof Error ? err.message : 'That did not save.');
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{existing ? existing.label : t('egs.new')}</h2>
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field
            label={t('common.name')}
            htmlFor="egs-label"
            required
            error={fields.label}
            hint={t('egs.labelHint')}
          >
            <TextInput
              id="egs-label"
              value={label}
              onChange={setLabel}
              placeholder={t('egs.mainBranch')}
              error={fields.label}
              autoFocus
            />
          </Field>

          {existing ? (
            <Field
              label={t('egs.howItSigns')}
              htmlFor="egs-arch-fixed"
              required
              hint={t('egs.architectureHint')}
            >
              <p className="ds-panel__body" id="egs-arch-fixed">
                {architectureName(existing.architecture)}
              </p>
            </Field>
          ) : (
            <Field
              label={t('egs.howItSigns')}
              htmlFor="egs-arch"
              required
              error={fields.architecture}
              hint={(() => {
                const found = architectures.find((a) => a.id === architecture);
                return found ? t(found.description) : undefined;
              })()}
            >
              <SelectInput
                id="egs-arch"
                value={architecture}
                onChange={(v) => setArchitecture(v as Architecture)}
                options={architectures}
                label={(a) => t(a.name)}
                error={fields.architecture}
              />
            </Field>
          )}

          {!central && (
            <Field
              label={t('common.branch')}
              htmlFor="egs-store"
              required
              error={fields.store_id}
              hint={t('egs.storeHint')}
            >
              <SelectInput
                id="egs-store"
                value={storeId}
                onChange={setStoreId}
                options={stores}
                label={(s) => s.name}
                placeholder={t('dev.chooseBranch')}
                error={fields.store_id}
              />
            </Field>
          )}
        </div>

        <div className="ds-panel__head">
          <h3 className="ds-h3">{t('egs.registrationDetails')}</h3>
          <p className="ds-caption">{t('egs.nineFields')}</p>
        </div>

        <div className="form__grid">
          <Field
            label={t('egs.unitNameForCert')}
            htmlFor="csr-cn"
            error={fields['csr.common_name']}
            hint={t('egs.commonNameHint')}
          >
            <TextInput
              id="csr-cn"
              value={csr.common_name}
              onChange={set('common_name')}
              error={fields['csr.common_name']}
            />
          </Field>

          <Field
            label={t('egs.serialNumber')}
            htmlFor="csr-serial"
            error={fields['csr.egs_serial_number']}
            hint={t('egs.serialHint')}
          >
            <TextInput
              id="csr-serial"
              value={csr.egs_serial_number}
              onChange={set('egs_serial_number')}
              placeholder="1-RawSyst|2-POS|3-000001"
              error={fields['csr.egs_serial_number']}
            />
          </Field>

          <Field
            label={t('common.vatNumber')}
            htmlFor="csr-vat"
            error={fields['csr.organization_identifier']}
            hint={t('egs.vatNumberHint')}
          >
            <TextInput
              id="csr-vat"
              value={csr.organization_identifier}
              onChange={set('organization_identifier')}
              inputMode="numeric"
              placeholder="300000000000003"
              error={fields['csr.organization_identifier']}
            />
          </Field>

          <Field
            label={t('egs.branchOrGroup')}
            htmlFor="csr-ou"
            error={fields['csr.organization_unit']}
            hint={t('egs.orgUnitHint')}
          >
            <TextInput
              id="csr-ou"
              value={csr.organization_unit}
              onChange={set('organization_unit')}
              error={fields['csr.organization_unit']}
            />
          </Field>

          <Field
            label={t('egs.registeredName')}
            htmlFor="csr-org"
            error={fields['csr.organization_name']}
            hint={t('egs.orgNameHint')}
          >
            <TextInput
              id="csr-org"
              value={csr.organization_name}
              onChange={set('organization_name')}
              error={fields['csr.organization_name']}
            />
          </Field>

          <Field label={t('common.country')} htmlFor="csr-country" error={fields['csr.country']}>
            <TextInput
              id="csr-country"
              value={csr.country}
              onChange={set('country')}
              placeholder="SA"
              error={fields['csr.country']}
            />
          </Field>

          <Field
            label={t('egs.invoicesIssued')}
            htmlFor="csr-type"
            error={fields['csr.invoice_type']}
            hint={t('egs.invoiceTypeHint')}
          >
            <SelectInput
              id="csr-type"
              value={csr.invoice_type}
              onChange={set('invoice_type')}
              options={invoiceTypes}
              label={(t) => t.name}
              placeholder={t('common.choose')}
              error={fields['csr.invoice_type']}
            />
          </Field>

          <Field
            label={t('common.address')}
            htmlFor="csr-location"
            error={fields['csr.location']}
            hint={t('egs.locationHint')}
          >
            <TextInput
              id="csr-location"
              value={csr.location}
              onChange={set('location')}
              error={fields['csr.location']}
            />
          </Field>

          <Field
            label={t('egs.industry')}
            htmlFor="csr-industry"
            error={fields['csr.industry']}
            hint={t('egs.industryHint')}
          >
            <TextInput
              id="csr-industry"
              value={csr.industry}
              onChange={set('industry')}
              error={fields['csr.industry']}
            />
          </Field>
        </div>

        <FormActions
          submitLabel={existing ? 'Save changes' : 'Add unit'}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

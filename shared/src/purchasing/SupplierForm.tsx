// Adding a supplier.
//
// Six fields, of which two are required. A supplier record that demanded a VAT
// number and a commercial registration up front would stop a buyer recording
// the man who delivers the boxes — and shops buy from that man constantly. So
// the form asks for a code and a name, and everything else is marked optional
// and means it.
//
// # Payment terms are the field that matters most
//
// They set when every bill from this supplier falls due, which drives the
// ageing report and therefore who gets chased. Given its own explanation for
// that reason, and defaulted to 0 — paying on delivery is the honest default
// for a shop that has not negotiated anything.

import { useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { createSupplier, type Supplier } from '../api/purchasing';
import { useAuth } from '../auth/session';
import {
  Field,
  FormActions,
  FormError,
  TextInput,
  type FieldErrors,
} from '../ui/Form';

export function SupplierForm({
  companyId,
  onSaved,
  onCancel,
}: {
  companyId: string;
  onSaved: (supplier: Supplier) => void;
  onCancel: () => void;
}) {
  const { client } = useAuth();

  const [code, setCode] = useState('');
  const [legalName, setLegalName] = useState('');
  const [terms, setTerms] = useState('0');
  const [vatNumber, setVatNumber] = useState('');
  const [phone, setPhone] = useState('');
  const [email, setEmail] = useState('');

  const [fields, setFields] = useState<FieldErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();

    // A cheap check to save a round trip, never the only one: the same
    // validation runs in Go and a test asserts it names every missing field.
    const local: FieldErrors = {};
    if (!code.trim()) local.code = 'Give the supplier a short code you will recognise.';
    if (!legalName.trim()) local.legal_name = "Enter the supplier's registered name.";
    const days = Number(terms);
    if (!Number.isInteger(days) || days < 0 || days > 365) {
      local.payment_terms_days = 'Payment terms run from 0 to 365 days.';
    }
    if (Object.keys(local).length > 0) {
      setFields(local);
      setFailure(null);
      return;
    }

    setBusy(true);
    setFields({});
    setFailure(null);
    try {
      onSaved(
        await createSupplier(client, companyId, {
          code: code.trim(),
          legal_name: legalName.trim(),
          payment_terms_days: days,
          vat_number: vatNumber.trim() || undefined,
          phone: phone.trim() || undefined,
          email: email.trim() || undefined,
        }),
      );
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          'This device cannot reach the server, so the supplier was not saved. ' +
            'Nothing has been lost — try again when the connection is back.',
        );
      } else if (err instanceof RequestFailed) {
        // Field-level messages go under their own inputs; anything else is a
        // banner. A duplicate code arrives here as a conflict.
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
        <h2 className="ds-h3">New supplier</h2>
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label="Short code" htmlFor="sup-code" required error={fields.code}
            hint="How you will find them in a list. SKU-style, e.g. ACME.">
            <TextInput id="sup-code" value={code} onChange={setCode}
              placeholder="ACME" error={fields.code} autoFocus />
          </Field>

          <Field label="Registered name" htmlFor="sup-name" required
            error={fields.legal_name}
            hint="The name on their invoices, not the trading name.">
            <TextInput id="sup-name" value={legalName} onChange={setLegalName}
              placeholder="Acme Textiles LLC" error={fields.legal_name} />
          </Field>

          <Field label="Payment terms" htmlFor="sup-terms" required
            error={fields.payment_terms_days}
            hint="Days from the invoice date. This sets when their bills fall due and who appears as overdue.">
            <TextInput id="sup-terms" value={terms} onChange={setTerms}
              inputMode="numeric" placeholder="30"
              error={fields.payment_terms_days} />
          </Field>

          <Field label="VAT number" htmlFor="sup-vat" error={fields.vat_number}
            hint="On their invoices. Needed to reclaim input tax on what you buy.">
            <TextInput id="sup-vat" value={vatNumber} onChange={setVatNumber}
              inputMode="numeric" error={fields.vat_number} />
          </Field>

          <Field label="Phone" htmlFor="sup-phone" error={fields.phone}>
            <TextInput id="sup-phone" value={phone} onChange={setPhone}
              inputMode="tel" error={fields.phone} />
          </Field>

          <Field label="Email" htmlFor="sup-email" error={fields.email}>
            <TextInput id="sup-email" value={email} onChange={setEmail}
              type="email" inputMode="email" error={fields.email} />
          </Field>
        </div>

        <FormActions submitLabel="Add supplier" busy={busy} onCancel={onCancel} />
      </div>
    </form>
  );
}

// Adding a customer.
//
// The mirror of SupplierForm, and deliberately the same shape: a code and a
// name required, everything else optional and meaning it. A shop that had to
// fill in an address before it could put a regular's name on a sale would stop
// recording regulars.
//
// # The credit limit is the field that carries the risk
//
// It is the ceiling on what this customer may owe, and B16 requires it enforced:
// a sale that would breach it is REFUSED at the till, not flagged. Left empty it
// means no credit account at all — which is the safe reading of an absent value,
// because a record typed in a hurry at the counter must not arrive with
// unlimited trust attached. The field says so rather than leaving somebody to
// discover it.
//
// It is also gated separately. `customers.set_credit_limit` is not part of
// `customers.manage`, so somebody who may correct a phone number may not decide
// how much the shop is prepared to be owed. When the permission is absent the
// field is not rendered — and the server refuses it regardless.

import { useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import {
  createCustomer,
  updateCustomer,
  type Customer,
  type CustomerType,
} from '../api/receivables';
import { useAuth } from '../auth/session';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
  type FieldErrors,
} from '../ui/Form';

const TYPES: { id: CustomerType; label: string }[] = [
  { id: 'retail', label: 'Retail' },
  { id: 'wholesale', label: 'Wholesale' },
  { id: 'vip', label: 'VIP' },
];

export function CustomerForm({
  companyId,
  existing,
  onSaved,
  onCancel,
}: {
  companyId: string;
  /** The customer being corrected. Absent when adding a new one. */
  existing?: Customer;
  onSaved: (customer: Customer) => void;
  onCancel: () => void;
}) {
  const { client, can } = useAuth();

  const [code, setCode] = useState(existing?.code ?? '');
  const [name, setName] = useState(existing?.name ?? '');
  const [type, setType] = useState<CustomerType>(existing?.customer_type ?? 'retail');
  const [terms, setTerms] = useState(String(existing?.payment_terms_days ?? 0));
  const [phone, setPhone] = useState(existing?.phone ?? '');
  const [email, setEmail] = useState(existing?.email ?? '');
  const [vatNumber, setVatNumber] = useState(existing?.vat_number ?? '');
  const [address, setAddress] = useState(existing?.address ?? '');
  const [creditLimit, setCreditLimit] = useState(existing?.credit_limit ?? '');

  const [fields, setFields] = useState<FieldErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const maySetLimit = can('customers.set_credit_limit');
  // On an existing customer the limit moves through its own screen, so it is
  // shown here only while creating. Two ways to change one number would let the
  // wrong permission win depending on which route somebody took.
  const showLimit = maySetLimit && !existing;

  async function submit(e: React.FormEvent) {
    e.preventDefault();

    // A cheap check to save a round trip, never the only one: the same
    // validation runs in Go.
    const local: FieldErrors = {};
    if (!existing && !code.trim()) {
      local.code = 'Give the customer a short code you will recognise.';
    }
    if (!name.trim()) local.name = "Enter the customer's name.";

    const days = Number(terms);
    if (!Number.isInteger(days) || days < 0 || days > 365) {
      local.payment_terms_days = 'Payment terms run from 0 to 365 days.';
    }
    if (showLimit && creditLimit.trim() !== '') {
      const limit = Number(creditLimit);
      if (!Number.isFinite(limit) || limit < 0) {
        local.credit_limit = 'A credit limit is an amount, or empty for no account.';
      }
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
      const details = {
        name: name.trim(),
        customer_type: type,
        payment_terms_days: days,
        phone: phone.trim() || undefined,
        email: email.trim() || undefined,
        vat_number: vatNumber.trim() || undefined,
        address: address.trim() || undefined,
      };
      onSaved(
        existing
          ? await updateCustomer(client, companyId, existing.id, details)
          : await createCustomer(client, companyId, {
              code: code.trim(),
              ...details,
              credit_limit: showLimit && creditLimit.trim() ? creditLimit.trim() : undefined,
            }),
      );
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          'This device cannot reach the server, so the customer was not saved. ' +
            'Nothing has been lost — try again when the connection is back.',
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
        <h2 className="ds-h3">{existing ? existing.name : 'New customer'}</h2>
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          {/* Read-only once set. The code appears on invoices already issued and
              signed, so renaming it would silently change what those documents
              refer to. */}
          <Field
            label="Short code"
            htmlFor="cust-code"
            required
            error={fields.code}
            hint={
              existing
                ? 'Fixed once set: it appears on invoices you have already issued.'
                : 'How you will find them in a list, e.g. NOOR.'
            }
          >
            {existing ? (
              <p className="field__fixed num">{existing.code}</p>
            ) : (
              <TextInput
                id="cust-code"
                value={code}
                onChange={setCode}
                placeholder="NOOR"
                error={fields.code}
                autoFocus
              />
            )}
          </Field>

          <Field label="Name" htmlFor="cust-name" required error={fields.name}
            hint="As it should appear on their invoices.">
            <TextInput id="cust-name" value={name} onChange={setName}
              placeholder="Al Noor Trading LLC" error={fields.name} />
          </Field>

          {/* Wholesale is kept apart in reporting (B12) so retail figures are
              not distorted by it. Which is why this is not cosmetic. */}
          <Field label="Type" htmlFor="cust-type" required error={fields.customer_type}
            hint="Wholesale is reported separately, so retail figures stay comparable.">
            <SelectInput
              id="cust-type"
              value={type}
              onChange={(v) => setType(v as CustomerType)}
              options={TYPES}
              label={(o) => o.label}
              error={fields.customer_type}
            />
          </Field>

          <Field label="Payment terms" htmlFor="cust-terms" required
            error={fields.payment_terms_days}
            hint="Days from the invoice date. This sets when their invoices fall due and who shows as overdue.">
            <TextInput id="cust-terms" value={terms} onChange={setTerms}
              inputMode="numeric" placeholder="30"
              error={fields.payment_terms_days} />
          </Field>

          {showLimit && (
            <Field label="Credit limit" htmlFor="cust-limit" error={fields.credit_limit}
              hint="The most they may owe at once. Leave empty for no credit account, which means every sale must be paid at the till.">
              <TextInput id="cust-limit" value={creditLimit} onChange={setCreditLimit}
                inputMode="decimal" placeholder="Leave empty for no credit"
                error={fields.credit_limit} />
            </Field>
          )}

          <Field label="Phone" htmlFor="cust-phone" error={fields.phone}
            hint="How a till finds them fastest.">
            <TextInput id="cust-phone" value={phone} onChange={setPhone}
              inputMode="tel" error={fields.phone} />
          </Field>

          <Field label="Email" htmlFor="cust-email" error={fields.email}>
            <TextInput id="cust-email" value={email} onChange={setEmail}
              type="email" inputMode="email" error={fields.email} />
          </Field>

          <Field label="VAT number" htmlFor="cust-vat" error={fields.vat_number}
            hint="Needed only if they require a full tax invoice rather than a simplified one.">
            <TextInput id="cust-vat" value={vatNumber} onChange={setVatNumber}
              inputMode="numeric" error={fields.vat_number} />
          </Field>

          <Field label="Address" htmlFor="cust-address" error={fields.address}>
            <TextInput id="cust-address" value={address} onChange={setAddress}
              error={fields.address} />
          </Field>
        </div>

        <FormActions
          submitLabel={existing ? 'Save changes' : 'Add customer'}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

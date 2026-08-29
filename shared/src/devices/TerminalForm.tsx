// Registering a terminal, and correcting one.
//
// Three fields, because that is genuinely all a terminal is before it is
// paired: a name somebody will recognise, the branch it stands in, and the
// e-invoicing unit it signs under. Everything else — operating system, version,
// when it last synced — is reported BY the terminal once it exists, and asking
// a shop owner to type any of it would be asking them to guess.
//
// # The e-invoicing unit is not optional
//
// A terminal's sales join its unit's invoice sequence, and the till resolves
// that unit on every sale. A terminal registered without one pairs, reports
// itself healthy and then refuses the first thing a cashier tries to sell. That
// is why the field is here rather than somewhere further along: the setup path
// is where a shop can still do something about it.
//
// # Moving a terminal is a correction, not a new terminal
//
// 04-identity §9: the ZATCA chain belongs to the device under its company's VAT
// registration and the ICV continues unbroken, so moving a till between
// branches of one business keeps its history. Moving it to another business is
// refused by the server, because that would change the registration the chain
// hangs from. The hint says so, in those words, because it is the one thing
// about this form that could surprise somebody.

import { useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import {
  amendTerminal,
  registerTerminal,
  type DeviceStore,
  type Terminal,
} from '../api/devices';
import type { EgsUnit } from '../api/egs';
import { unitsForStore } from '../einvoicing/egs';
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

export function TerminalForm({
  companyId,
  stores,
  units,
  existing,
  onSaved,
  onCancel,
}: {
  companyId: string;
  stores: DeviceStore[];
  /** The e-invoicing units this business has. Empty means none has been set up
   *  yet, and the form says so instead of offering an empty picker. */
  units: EgsUnit[];
  /** The terminal being corrected. Absent when registering a new one. */
  existing?: Terminal;
  onSaved: (terminal: Terminal) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [label, setLabel] = useState(existing?.terminal_label ?? '');
  const [storeId, setStoreId] = useState(
    existing?.store_id ?? (stores.length === 1 ? stores[0]!.id : ''),
  );
  const [unitId, setUnitId] = useState(existing?.egs_unit_id ?? '');

  const [fields, setFields] = useState<FieldErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // A unit tied to another branch cannot sign for this till and the server
  // refuses the binding, so it is not offered. Recomputed as the branch changes
  // rather than filtered once, or moving the till would leave a stale choice
  // selected that only fails on save.
  const eligible = unitsForStore(units, storeId, existing?.egs_unit_id);
  const chosenIsEligible = !unitId || eligible.some((u) => u.id === unitId);

  async function submit(e: React.FormEvent) {
    e.preventDefault();

    // A cheap check to save a round trip, never the only one: the same
    // validation runs in Go.
    const local: FieldErrors = {};
    if (!label.trim()) {
      local.terminal_label = 'Give the terminal a name, like "Till 2".';
    }
    if (!storeId) local.store_id = t('terminal.chooseBranch');
    // Required when registering. When correcting an older terminal that never
    // had one, leaving it blank keeps things as they are rather than blocking
    // a rename behind a decision the reader came here for something else.
    if (!existing && !unitId) {
      local.egs_unit_id = t('terminal.chooseUnit');
    }
    if (unitId && !chosenIsEligible) {
      local.egs_unit_id = t('terminal.unitOtherBranch');
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
        existing
          ? await amendTerminal(client, companyId, existing.id, {
              terminal_label: label.trim(),
              store_id: storeId,
              egs_unit_id: unitId || undefined,
            })
          : await registerTerminal(client, companyId, {
              store_id: storeId,
              terminal_label: label.trim(),
              egs_unit_id: unitId,
            }),
      );
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          t('terminal.saveOffline') +
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
        <h2 className="ds-h3">
          {existing ? existing.terminal_label : t('terminal.new')}
        </h2>
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field
            label={t('common.name')}
            htmlFor="term-label"
            required
            error={fields.terminal_label}
            hint={t('terminal.labelHint')}
          >
            <TextInput
              id="term-label"
              value={label}
              onChange={setLabel}
              placeholder={t('devices.tillNamePlaceholder')}
              error={fields.terminal_label}
              autoFocus
            />
          </Field>

          <Field
            label={t('common.branch')}
            htmlFor="term-store"
            required
            error={fields.store_id}
            hint={
              existing
                ? 'Moving a terminal between your branches keeps its invoice history. It cannot move to another business.'
                : t('terminal.whereItStands')
            }
          >
            <SelectInput
              id="term-store"
              value={storeId}
              onChange={setStoreId}
              options={stores}
              label={(s) => s.name}
              placeholder={t('dev.chooseBranch')}
              error={fields.store_id}
            />
          </Field>

          <Field
            label={t('dev.einvoicingUnit')}
            htmlFor="term-egs"
            required={!existing}
            error={fields.egs_unit_id}
            hint={
              units.length === 0
                ? 'This business has no e-invoicing unit yet. Add one under E-invoicing first — a terminal without one cannot ring up a sale.'
                : 'What signs this terminal\u2019s invoices and keeps them in one numbered sequence.'
            }
          >
            <SelectInput
              id="term-egs"
              value={unitId}
              onChange={setUnitId}
              options={eligible}
              label={(u) => u.label}
              placeholder={t('dev.chooseUnit')}
              error={fields.egs_unit_id}
            />
          </Field>
        </div>

        <FormActions
          submitLabel={existing ? 'Save changes' : 'Add terminal'}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

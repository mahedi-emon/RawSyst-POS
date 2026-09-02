// The form a shop types its own acquirer credentials into (blueprint E3.3).
//
// # Every box on this form comes from the server
//
// Nothing here knows that Moyasar wants a publishable key and PayTabs wants a
// profile id and a region. The server answers `GET /payment-providers` with
// each acquirer and the fields it needs, and this renders a box per field. A
// shop picks its provider, the right boxes appear, it pastes what its own
// dashboard gave it, presses Test, and the till takes cards.
//
// That is why adding an acquirer is an adapter and a table entry rather than a
// change in this file.
//
// # The secret box is empty on an edit, and that is not a bug
//
// A stored key is never returned by any route, so this could not show it even
// as dots. An empty box on save means "leave what is stored", which is said
// under the field rather than left to be discovered.

import { useMemo, useState, type FormEvent } from 'react';

import {
  saveGateway,
  type Gateway,
  type GatewayInput,
  type PaymentProvider,
} from '../api/payments';
import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { tenderName } from '../ui/format';

export function GatewayForm({
  companyId,
  providers,
  editing,
  onDone,
  onCancel,
}: {
  companyId: string;
  providers: PaymentProvider[];
  /** The connection being edited, or undefined for a new one. */
  editing?: Gateway;
  onDone: () => void;
  onCancel: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [providerKey, setProviderKey] = useState(
    editing?.provider ?? providers[0]?.key ?? '',
  );
  const [label, setLabel] = useState(editing?.label ?? '');
  const [mode, setMode] = useState<'test' | 'live'>(editing?.mode ?? 'test');
  const [settings, setSettings] = useState<Record<string, string>>(
    editing?.settings ?? {},
  );
  const [secret, setSecret] = useState('');
  const [methods, setMethods] = useState<string[]>(editing?.methods ?? []);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const provider = useMemo(
    () => providers.find((p) => p.key === providerKey),
    [providers, providerKey],
  );

  // Switching provider clears what was typed for the previous one: a profile
  // id left over from PayTabs is not a Moyasar key, and carrying it forward
  // would be a configuration that fails at the counter.
  function chooseProvider(key: string) {
    setProviderKey(key);
    setSettings({});
    setSecret('');
    setMethods(providers.find((p) => p.key === key)?.methods ?? []);
  }

  function toggleMethod(method: string) {
    setMethods((current) =>
      current.includes(method)
        ? current.filter((m) => m !== method)
        : [...current, method],
    );
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    const input: GatewayInput = {
      provider: providerKey,
      label,
      mode,
      settings,
      secret,
      methods,
      // A new connection is never switched on here. It has to pass a test
      // first, which the table's own rule enforces for a live one — so the
      // form does not offer a switch that would only be refused.
      is_active: editing?.is_active ?? false,
    };
    try {
      await saveGateway(client, companyId, input, editing?.id);
      onDone();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="ds-panel pay__form"
      onSubmit={(e) => void submit(e)}
      noValidate
    >
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">
            {editing ? t('pay.editConnection') : t('pay.connect')}
          </h2>
          <p className="ds-caption">{t('pay.connectHint')}</p>
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        <Field label={t('pay.provider')} htmlFor="pay-provider" required>
          <select
            id="pay-provider"
            className="input"
            value={providerKey}
            // A saved connection keeps its provider: changing it would leave
            // the stored key belonging to somebody else's API.
            disabled={Boolean(editing)}
            onChange={(e) => chooseProvider(e.target.value)}
          >
            {providers.map((p) => (
              <option key={p.key} value={p.key}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>

        <Field
          label={t('pay.label')}
          hint={t('pay.labelHint')}
          htmlFor="pay-label"
          required
        >
          <TextInput id="pay-label" value={label} onChange={setLabel} />
        </Field>

        <Field
          label={t('pay.mode')}
          hint={t('pay.modeHint')}
          htmlFor="pay-mode"
          required
        >
          <select
            id="pay-mode"
            className="input"
            value={mode}
            onChange={(e) =>
              setMode(e.target.value === 'live' ? 'live' : 'test')
            }
          >
            <option value="test">{t('pay.modeTest')}</option>
            <option value="live">{t('pay.modeLive')}</option>
          </select>
        </Field>

        {/* The boxes the chosen provider asked for. */}
        {provider?.fields.map((f) =>
          f.secret ? (
            <Field
              key={f.key}
              label={f.label}
              hint={editing?.has_secret ? t('pay.secretKept') : f.hint}
              htmlFor={`pay-${f.key}`}
              required={!editing?.has_secret}
            >
              <TextInput
                id={`pay-${f.key}`}
                type="password"
                autoComplete="off"
                value={secret}
                onChange={setSecret}
              />
            </Field>
          ) : (
            <Field
              key={f.key}
              label={f.label}
              hint={f.hint}
              htmlFor={`pay-${f.key}`}
              required
            >
              <TextInput
                id={`pay-${f.key}`}
                value={settings[f.key] ?? ''}
                onChange={(v) =>
                  setSettings((current) => ({ ...current, [f.key]: v }))
                }
              />
            </Field>
          ),
        )}

        {provider && provider.methods.length > 0 && (
          <fieldset className="pay__methods">
            <legend className="field__label">{t('pay.methods')}</legend>
            <p className="field__hint">{t('pay.methodsHint')}</p>
            <div className="pay__methodList">
              {provider.methods.map((m) => (
                <label key={m} className="pay__method">
                  <input
                    type="checkbox"
                    checked={methods.includes(m)}
                    onChange={() => toggleMethod(m)}
                  />{' '}
                  {tenderName(m, t)}
                </label>
              ))}
            </div>
          </fieldset>
        )}

        {provider?.docs && (
          <p className="ds-caption">
            {t('pay.whereToFind')}{' '}
            <span className="pay__docs">{provider.docs}</span>
          </p>
        )}

        <FormActions
          busy={busy}
          disabled={label.trim() === '' || providerKey === ''}
          submitLabel={t('action.save')}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

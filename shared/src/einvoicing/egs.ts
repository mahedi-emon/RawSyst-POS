// The E-invoicing screen's own decisions, separated from its rendering.
//
// Four things here decide something: how to describe a unit's certification
// state, which of the nine CSR fields are still missing, which units a given
// terminal is allowed to sign under, and what to say to somebody whose till
// cannot sell. Each is a place where a wrong answer either misleads a shop
// about its ZATCA position or leaves a working till idle.
//
// Nothing here asserts a ZATCA rule. The two format checks mirror the check
// constraints the database already enforces, and exist so a reader is told
// before they save rather than after.

import type { Architecture, Csr, EgsUnit } from '../api/egs';
import type { Terminal } from '../api/devices';

/** The three architectures, in the words a shop owner uses.
 *
 *  The description is not decoration: it is the only thing on the form that
 *  explains a choice which cannot be changed afterwards, because it decides
 *  where the signing key lives. */
export const architectures: Array<{
  id: Architecture;
  name: string;
  description: string;
}> = [
  {
    id: 'smart_pos',
    name: 'The till signs for itself',
    description:
      'Each till holds its own certificate and its own invoice sequence. The usual choice for a shop with a few counters.',
  },
  {
    id: 'branch_server',
    name: 'One server for a branch',
    description:
      'A computer in the branch signs for every till in it. The tills hold no certificate of their own.',
  },
  {
    id: 'centralized_server',
    name: 'One server for the whole business',
    description:
      'A single system signs for every branch. Choose this only if your invoices are already generated centrally.',
  },
];

export function architectureName(a: Architecture): string {
  return architectures.find((x) => x.id === a)?.name ?? a;
}

/** Which invoices a unit issues. The four digits are ZATCA's functionality map
 *  and the last two are reserved, which is why only three options exist. */
export const invoiceTypes = [
  { id: '1100', name: 'Both standard and simplified invoices' },
  { id: '1000', name: 'Standard invoices only (business customers)' },
  { id: '0100', name: 'Simplified invoices only (walk-in customers)' },
];

export interface UnitState {
  label: string;
  tone: 'neutral' | 'info' | 'success' | 'warning' | 'danger';
  /** One sentence saying what to do next, or nothing when there is nothing to
   *  do. Absent for a live unit and for one whose CSR is not ready yet, since
   *  the missing-fields list says that better. */
  next?: string;
}

/** What a unit's certification state means, in plain words.
 *
 *  `not_started` is the state every unit is in today, and it deliberately does
 *  NOT read as an error: onboarding is not built, so a shop has done nothing
 *  wrong by not having done it. */
export function describeUnit(u: EgsUnit): UnitState {
  switch (u.csid_status) {
    case 'live':
      return { label: 'Registered with ZATCA', tone: 'success' };
    case 'production_csid':
      return { label: 'Production certificate issued', tone: 'success' };
    case 'compliance_csid':
      return {
        label: 'Passed compliance testing',
        tone: 'info',
        next: 'The next step is the production certificate.',
      };
    case 'revoked':
      return {
        label: 'Certificate revoked',
        tone: 'danger',
        next: 'This unit cannot sign. Its past invoices stay valid and readable.',
      };
    case 'expired':
      return {
        label: 'Certificate expired',
        tone: 'danger',
        next: 'This unit cannot sign until its certificate is renewed.',
      };
    default:
      return { label: 'Not registered yet', tone: 'neutral' };
  }
}

/** The nine CSR fields, in the order the form asks for them, with the label
 *  each is shown under. Exported so the "still missing" list and the form
 *  cannot drift into naming the same field two ways. */
export const csrFields: Array<{ key: keyof Csr; label: string }> = [
  { key: 'common_name', label: 'Unit name' },
  { key: 'egs_serial_number', label: 'Serial number' },
  { key: 'organization_identifier', label: 'VAT number' },
  { key: 'organization_unit', label: 'Branch or group member' },
  { key: 'organization_name', label: 'Registered business name' },
  { key: 'country', label: 'Country' },
  { key: 'invoice_type', label: 'Invoices issued' },
  { key: 'location', label: 'Address' },
  { key: 'industry', label: 'Industry' },
];

/** Which of the nine are still blank.
 *
 *  All nine are required to register with ZATCA, and none is required to save a
 *  unit — a shop should be able to set a till up today and find its industry
 *  classification tomorrow. So the screen lists what is outstanding rather than
 *  refusing the save. */
export function missingCsrFields(csr: Csr): string[] {
  return csrFields.filter((f) => !csr[f.key]?.trim()).map((f) => f.label);
}

/** Mirrors of the two database check constraints, so a reader is told about a
 *  mistyped VAT number while they are still looking at the field. */
export function vatNumberProblem(value: string): string | null {
  const v = value.trim();
  if (!v) return null;
  return /^3[0-9]{13}3$/.test(v)
    ? null
    : 'A Saudi VAT number is 15 digits and starts and ends with 3.';
}

/** A VAT number whose 11th digit is 1 belongs to a VAT group, and the branch
 *  field must then carry the 10-digit tax number of the member being
 *  registered instead of a branch name. */
export function isVatGroup(organizationIdentifier: string): boolean {
  const v = organizationIdentifier.trim();
  return v.length === 15 && v[10] === '1';
}

export function organizationUnitProblem(csr: Csr): string | null {
  const unit = csr.organization_unit.trim();
  if (!unit || !isVatGroup(csr.organization_identifier)) return null;
  return /^[0-9]{10}$/.test(unit)
    ? null
    : 'This VAT number belongs to a VAT group, so this must be the 10-digit tax number of the member being registered.';
}

/** The units a terminal in `storeId` may sign under.
 *
 *  A central unit covers every branch. A branch server and a smart POS sign for
 *  one branch, so a till elsewhere has no business picking one — and the server
 *  refuses that binding, so offering it here would only produce a refusal the
 *  reader cannot act on.
 *
 *  `keepId` is the unit the terminal already has. It stays on the list wherever
 *  it belongs, because a till that has moved between branches keeps its chain
 *  and dropping its own unit out of the picker would read as an instruction to
 *  repoint it — which is the one thing that would break the chain. */
export function unitsForStore(
  units: EgsUnit[],
  storeId: string,
  keepId?: string,
): EgsUnit[] {
  return units.filter(
    (u) =>
      (keepId && u.id === keepId) ||
      !u.store_id ||
      (!!storeId && u.store_id === storeId),
  );
}

/** Why a terminal cannot sell, or nothing when it can.
 *
 *  Only the e-invoicing half is answered here. Pairing and being switched on
 *  are already described by devices.describeState, and repeating them would
 *  give a reader two sentences about the same till that could disagree. */
export function sellingBlocked(t: Terminal): string | null {
  if (t.status === 'revoked') return null;
  if (!t.egs_unit_id) {
    return 'This terminal is not linked to an e-invoicing unit, so it cannot ring up a sale. Edit it and choose one.';
  }
  return null;
}

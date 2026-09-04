// Where a business keeps its money, and what moves between those places.
//
// Five kinds, matching migration 0081's CHECK. Three of them can carry bank
// detail and two cannot — a till float has no IBAN — and the form follows that
// rather than showing every field for every kind.

import type { Tone } from '@/components/ui/panel';
import type { Key } from '@/lib/i18n/locale';

export interface MoneyAccount {
  id: string;
  kind: string;
  name: string;
  name_ar?: string;
  currency: string;
  store?: string;
  is_active: boolean;
  /** The ledger account it posts to. */
  account_id: string;
  account_code: string;
  bank_name?: string;
  account_number?: string;
  iban?: string;
  swift?: string;
  balance: string;
  /** Statement lines nobody has matched yet. Absent when there are none. */
  unreconciled?: number;
}

export interface Transfer {
  id: string;
  reference?: string;
  from_account: string;
  to_account: string;
  amount: string;
  currency: string;
  moved_on: string;
  note?: string;
  created_by?: string;
}

export const ACCOUNT_KIND: Record<string, { key: Key; tone: Tone }> = {
  cash: { key: 'nx.acc.kCash', tone: 'neutral' },
  petty_cash: { key: 'nx.acc.kPettyCash', tone: 'neutral' },
  bank: { key: 'nx.acc.kBank', tone: 'info' },
  card_settlement: { key: 'nx.acc.kCardSettlement', tone: 'info' },
  gateway: { key: 'nx.acc.kGateway', tone: 'info' },
};

export const ACCOUNT_KINDS = [
  'cash',
  'petty_cash',
  'bank',
  'card_settlement',
  'gateway',
] as const;

/**
 * Whether this kind of account can carry bank detail.
 *
 * The same three the schema allows it on. A till float has no IBAN, and a form
 * that asked for one would be asking for a fact that does not exist.
 */
export function isBankLike(kind: string): boolean {
  return kind === 'bank' || kind === 'card_settlement' || kind === 'gateway';
}

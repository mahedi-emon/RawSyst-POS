// The approval engine, as the screens see it.
//
// # Only the subjects the engine actually evaluates
//
// `approval_rule.subject` is free text in the database, and a picker offering
// every noun in the product would let somebody configure a rule against
// "sales_order" that no code path will ever ask about. They would then wait for
// an approval that never arrives, and nothing anywhere would say why.
//
// Two call sites reach the engine today — `expenses.gate` with `"expense"` and
// `purchasing` with `"purchase_order"` — so those are the two on offer. When a
// third subject starts calling `Evaluate`, it is added here, and until then a
// rule about it would be a lie the screen told.
//
// # And only the conditions each subject supplies
//
// `matches` is explicit that a clause naming a fact the caller did not supply
// does NOT match — the safe direction, because the alternative is a rule
// silently applying everywhere. The consequence for a configuration screen is
// that offering a store threshold on a purchase order produces a rule which can
// never fire, and the person who wrote it has no way to discover that.
//
// So each subject declares what it knows:
//
//   - an expense supplies its gross amount and, when one was named, its store;
//     its `At` is the expense DATE, so the hour is always midnight and a
//     time-of-day clause could never match
//   - a purchase order supplies its total and the real clock, and deliberately
//     no store: the order names a warehouse, and offering that as a store would
//     make a branch rule match something written for a different thing
//
// `percent_over`, `quantity_over`, `employee_id` and `category_id` are read by
// the engine and supplied by nothing today. They are not offered.

/** A subject the engine is actually asked about. */
export type Subject = 'expense' | 'purchase_order';

/** A clause a rule may carry. Every one present must hold; they are ANDed. */
export type Clause =
  | 'amount_over'
  | 'amount_under'
  | 'store_id'
  | 'after_hour'
  | 'before_hour';

/** What a rule does when its condition holds. */
export type Action =
  | 'require_approval'
  | 'require_pin'
  | 'block'
  | 'warn'
  | 'notify';

export const ACTIONS: readonly Action[] = [
  'require_approval',
  'require_pin',
  'block',
  'warn',
  'notify',
];

export const SUBJECTS: readonly Subject[] = ['expense', 'purchase_order'];

/** What each subject can be asked about. */
export const CLAUSES_FOR: Record<Subject, readonly Clause[]> = {
  expense: ['amount_over', 'amount_under', 'store_id'],
  purchase_order: ['amount_over', 'amount_under', 'after_hour', 'before_hour'],
};

/** One signature a rule collects, in order. */
export interface Step {
  /** A role key. The engine matches a cloned role by the role it was copied
   *  from, so a step naming a system role also covers every copy of it. */
  role?: string;
  /** Or one named person, and whoever is covering for them today. */
  user_id?: string;
}

/** A rule as the API returns it. `condition` and `steps` arrive as JSON text. */
export interface RuleRow {
  id: string;
  name: string;
  is_active: boolean;
  subject: string;
  /** A JSON object. `{}` means the rule always fires. */
  condition: string;
  action: string;
  /** A JSON array of steps. Empty unless the action requires approval. */
  steps: string;
  escalate_after_hours?: number;
  priority: number;
}

/** A rule's condition, decoded. Every value is a string as the engine reads
 *  thresholds through `decimal.NewFromString`; the hours are numbers. */
export interface Condition {
  amount_over?: string;
  amount_under?: string;
  store_id?: string;
  after_hour?: number;
  before_hour?: number;
}

/**
 * Decodes a rule's condition, tolerating anything.
 *
 * The column is `jsonb` with only an "is an object" check on it, so a rule
 * written by an older version of this screen — or by hand — may carry a clause
 * this one does not know. Unknown keys are dropped rather than crashing the
 * list, and the row still renders with the clauses it does understand.
 */
export function decodeCondition(raw: string): Condition {
  try {
    const parsed: unknown = JSON.parse(raw || '{}');
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return {};
    }
    const source = parsed as Record<string, unknown>;
    const out: Condition = {};
    for (const key of ['amount_over', 'amount_under', 'store_id'] as const) {
      const value = source[key];
      if (typeof value === 'string' && value !== '') out[key] = value;
    }
    for (const key of ['after_hour', 'before_hour'] as const) {
      const value = source[key];
      if (typeof value === 'number' && Number.isFinite(value)) out[key] = value;
    }
    return out;
  } catch {
    return {};
  }
}

/** Decodes a rule's steps, dropping any that name nobody. */
export function decodeSteps(raw: string): Step[] {
  try {
    const parsed: unknown = JSON.parse(raw || '[]');
    if (!Array.isArray(parsed)) return [];
    const out: Step[] = [];
    for (const entry of parsed) {
      if (typeof entry !== 'object' || entry === null) continue;
      const step = entry as Record<string, unknown>;
      const role = typeof step.role === 'string' ? step.role : '';
      const userID = typeof step.user_id === 'string' ? step.user_id : '';
      if (role === '' && userID === '') continue;
      out.push(role !== '' ? { role } : { user_id: userID });
    }
    return out;
  } catch {
    return [];
  }
}

/** True for a subject the engine is actually asked about. */
export function isSubject(value: string): value is Subject {
  return (SUBJECTS as readonly string[]).includes(value);
}

/** One arrangement of who decides while somebody is away. */
export interface Cover {
  id: string;
  from: string;
  to: string;
  from_user_id: string;
  to_user_id: string;
  starts_on: string;
  ends_on: string;
  note?: string;
  /** True while it is in force today. The server computes it, so the screen
   *  and the engine cannot disagree about whether cover is live. */
  live: boolean;
}

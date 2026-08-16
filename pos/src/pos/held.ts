// Parking a sale and picking it up again.
//
// The customer who left their wallet in the car, the one waiting on a price
// check, the one who wants to add something from aisle four. Without this the
// cashier either holds the queue or voids the cart, and a voided cart is a
// customer who walks.
//
// # Entirely local, and never sent
//
// A held cart is not a sale. It has no invoice UUID, consumes no ICV, touches
// no stock and produces no journal entry — it is a note on the terminal about
// what somebody was buying. It becomes a sale only when it is resumed and
// finished, at which point it goes through the ordinary queue like any other.
//
// That is why it is not in the sync queue and never reaches the server. Pushing
// held carts would mean the server holding rows that look like sales and are
// not, and would raise the question of what happens when two terminals resume
// the same one. Blueprint plan ceilings cap held carts per terminal precisely
// because they are a local resource.
//
// # Which means an abandoned one is rubbish that must be swept
//
// A cart held on Friday and never resumed is not a pending sale to be worried
// about at close; it is a customer who left. They expire, so the list a cashier
// sees on Monday is not last week's ghosts.

import type { CartLine, CartTender } from './cart';

export interface HeldCart {
  id: string;
  /** What the cashier will recognise it by — a name, a phone number, "blue
   *  jacket". Optional, because the common case is a cashier in a hurry. */
  label: string;
  lines: CartLine[];
  tenders: CartTender[];
  heldAt: string;
  /** Shown in the list so a cashier can see at a glance which is which. */
  total: string;
  itemCount: number;
}

/** The storage this needs, kept an interface so the tests run without Tauri. */
export interface HeldCartStore {
  put(cart: HeldCart): Promise<void>;
  list(): Promise<HeldCart[]>;
  take(id: string): Promise<HeldCart | null>;
  purgeBefore(cutoff: string): Promise<number>;
  count(): Promise<number>;
}

/** How many a terminal may hold at once.
 *
 * A concrete ceiling rather than "unlimited", per the architecture rule that
 * every unlimited claim becomes a number. A till with three hundred held carts
 * has a process problem, and the list stops being usable long before that. */
export const MAX_HELD = 20;

/** How long before an unresumed cart is swept. Longer than a shift, shorter
 *  than a weekend: a cart held at 9pm should still be there for the closing
 *  cashier, and gone by the time the shop opens on Monday. */
export const HOLD_TTL_MS = 24 * 60 * 60 * 1000;

export class HeldCarts {
  constructor(
    private readonly store: HeldCartStore,
    private readonly now: () => Date = () => new Date(),
  ) {}

  /**
   * Parks the current cart.
   *
   * Refuses an empty one — an empty held cart is a cashier who pressed the
   * wrong button, and putting it in the list only makes the real ones harder
   * to find.
   */
  async hold(
    lines: CartLine[],
    tenders: CartTender[],
    label: string,
    total: string,
  ): Promise<HeldCart> {
    if (lines.length === 0) {
      throw new Error('There is nothing to hold.');
    }
    if ((await this.store.count()) >= MAX_HELD) {
      throw new Error(
        `This terminal is already holding ${MAX_HELD} sales. ` +
          'Finish or discard one before holding another.',
      );
    }

    const cart: HeldCart = {
      id: crypto.randomUUID(),
      label: label.trim(),
      // Copied, not referenced. The cashier clears the counter immediately
      // after holding, and a shared array would empty the held cart with it.
      lines: lines.map((l) => ({ ...l })),
      tenders: tenders.map((t) => ({ ...t })),
      heldAt: this.now().toISOString(),
      total,
      itemCount: lines.length,
    };
    await this.store.put(cart);
    return cart;
  }

  /** Newest first: the cart most likely to be resumed is the one held last. */
  list(): Promise<HeldCart[]> {
    return this.store.list();
  }

  /**
   * Takes a cart back out, removing it from the list.
   *
   * Removed on resume rather than on finish. Leaving it until the sale
   * completed would let a second cashier resume the same cart from a second
   * till and ring it up twice — two invoices, two stock movements, one
   * customer. A cart the cashier then abandons is re-held, which is one extra
   * press in the rare case, against a double sale in the bad one.
   */
  resume(id: string): Promise<HeldCart | null> {
    return this.store.take(id);
  }

  /** Sweeps carts nobody came back for. */
  purgeExpired(): Promise<number> {
    const cutoff = new Date(this.now().getTime() - HOLD_TTL_MS).toISOString();
    return this.store.purgeBefore(cutoff);
  }
}

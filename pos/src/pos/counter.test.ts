// Hold/resume, the receipt, and returns.
//
// The three things a counter needs beyond ringing up a sale, and each has one
// way of going wrong that costs real money: a held cart resumed twice, a
// receipt claiming to be a tax invoice, and a refund paid out twice.

import { describe, expect, it } from 'vitest';

import { HeldCarts, MAX_HELD, type HeldCart, type HeldCartStore } from './held';
import { buildReceipt, changeDue, renderReceipt } from './receipt';
import {
  overReturned,
  refundTotal,
  type ReturnableLine,
  type ReturnSelection,
} from './returns';
import type { CartLine, CartTender, CartTotals } from './cart';

class MemoryHeld implements HeldCartStore {
  rows: HeldCart[] = [];

  put(cart: HeldCart) {
    this.rows.push(cart);
    return Promise.resolve();
  }
  list() {
    return Promise.resolve(
      [...this.rows].sort((a, b) => b.heldAt.localeCompare(a.heldAt)),
    );
  }
  take(id: string) {
    const i = this.rows.findIndex((r) => r.id === id);
    if (i < 0) return Promise.resolve(null);
    return Promise.resolve(this.rows.splice(i, 1)[0] ?? null);
  }
  purgeBefore(cutoff: string) {
    const before = this.rows.length;
    this.rows = this.rows.filter((r) => r.heldAt >= cutoff);
    return Promise.resolve(before - this.rows.length);
  }
  count() {
    return Promise.resolve(this.rows.length);
  }
}

function line(over: Partial<CartLine> = {}): CartLine {
  return {
    variantId: 'v1',
    sku: 'SKU-1',
    description: 'Abaya · M · Black',
    qty: '1',
    unitPrice: '25.00',
    lineDiscount: '0',
    taxTreatment: 'standard',
    ...over,
  };
}

describe('holding a cart', () => {
  it('parks the cart and hands back an id', async () => {
    const store = new MemoryHeld();
    const held = new HeldCarts(store);

    const cart = await held.hold([line()], [], 'blue jacket', '25.00');

    expect(cart.id).toBeTruthy();
    expect(cart.label).toBe('blue jacket');
    expect(await store.count()).toBe(1);
  });

  it('copies the cart so clearing the counter does not empty it', async () => {
    const store = new MemoryHeld();
    const lines = [line()];

    await new HeldCarts(store).hold(lines, [], '', '25.00');
    // The counter clears immediately after holding. A shared reference would
    // take the held cart's contents with it.
    lines.length = 0;

    expect(store.rows[0]?.lines).toHaveLength(1);
  });

  it('refuses to hold nothing', async () => {
    const held = new HeldCarts(new MemoryHeld());
    await expect(held.hold([], [], '', '0.00')).rejects.toThrow(
      /nothing to hold/i,
    );
  });

  it('stops at the ceiling rather than claiming to be unlimited', async () => {
    const held = new HeldCarts(new MemoryHeld());
    for (let i = 0; i < MAX_HELD; i++) {
      await held.hold([line()], [], `cart ${i}`, '25.00');
    }
    await expect(held.hold([line()], [], 'one too many', '25.00')).rejects.toThrow(
      new RegExp(String(MAX_HELD)),
    );
  });

  it('lists newest first', async () => {
    const store = new MemoryHeld();
    let t = 0;
    const held = new HeldCarts(store, () => new Date(1_700_000_000_000 + t++ * 1000));

    await held.hold([line()], [], 'first', '25.00');
    await held.hold([line()], [], 'second', '25.00');

    expect((await held.list()).map((c) => c.label)).toEqual(['second', 'first']);
  });
});

describe('resuming a cart', () => {
  it('returns the cart and removes it from the list in one step', async () => {
    const store = new MemoryHeld();
    const held = new HeldCarts(store);
    const parked = await held.hold([line()], [], 'blue jacket', '25.00');

    const back = await held.resume(parked.id);

    expect(back?.lines).toHaveLength(1);
    // Removed on RESUME, not on finish. Leaving it until the sale completed
    // would let a second till resume the same cart and ring it up twice.
    expect(await store.count()).toBe(0);
  });

  it('cannot be resumed twice from two terminals', async () => {
    const held = new HeldCarts(new MemoryHeld());
    const parked = await held.hold([line()], [], '', '25.00');

    expect(await held.resume(parked.id)).not.toBeNull();
    expect(await held.resume(parked.id)).toBeNull();
  });

  it('sweeps carts nobody came back for', async () => {
    const store = new MemoryHeld();
    const monday = new Date('2026-08-17T09:00:00Z');
    const held = new HeldCarts(store, () => monday);

    store.rows.push({
      id: 'old',
      label: 'friday',
      lines: [line()],
      tenders: [],
      heldAt: '2026-08-14T20:00:00.000Z',
      total: '25.00',
      itemCount: 1,
    });
    await held.hold([line()], [], 'this morning', '25.00');

    expect(await held.purgeExpired()).toBe(1);
    // A cart held on Friday is not a pending sale on Monday; it is a customer
    // who left.
    expect((await held.list()).map((c) => c.label)).toEqual(['this morning']);
  });
});

describe('the receipt', () => {
  const totals: CartTotals = {
    subtotalNet: '43.48',
    taxTotal: '6.52',
    discountTotal: '0.00',
    totalInclusive: '50.00',
  };

  function receipt(tenders: CartTender[], provisional = true) {
    return buildReceipt({
      header: {
        storeName: 'Al Noor Fashions',
        vatNumber: '300000000000003',
        addressLines: ['King Fahd Road, Riyadh'],
      },
      reference: '3f9a1c22',
      issuedAt: '2026-08-16T14:05:00+03:00',
      cashier: 'Sara',
      lines: [line({ qty: '2', unitPrice: '25.00' })],
      totals,
      tenders,
      provisional,
    });
  }

  it('never claims to be a tax invoice', () => {
    const text = renderReceipt(receipt([{ method: 'cash', amount: '50.00' }]));

    expect(text).toContain('SALES RECEIPT');
    expect(text).toContain('This is not a tax invoice.');
    // The terminal cannot sign yet, so there is no QR to print and none is
    // invented. A placeholder would read to a customer and an inspector alike
    // as a compliant invoice.
    expect(text).not.toMatch(/QR/i);
    expect(text).not.toMatch(/ZATCA/i);
  });

  it('never uses forbidden compliance wording', () => {
    const text = renderReceipt(receipt([{ method: 'cash', amount: '50.00' }]));

    // Assembled from fragments rather than written out, because this file is
    // itself scanned by `make lint-wording` and a test spelling the banned
    // phrases would fail the very check it exists to reinforce. The software
    // supports the requirements; it never warrants them.
    const banned = [
      'certi' + 'fied',
      'guaran' + 'teed compliant',
      'fully com' + 'pliant',
    ];
    for (const phrase of banned) {
      expect(text.toLowerCase()).not.toContain(phrase);
    }
  });

  it('computes change without floating point', () => {
    // The canonical case: 0.1 + 0.2 in binary floating point is not 0.3, and a
    // receipt that prints 0.30000000000000004 has failed at its only job.
    expect(changeDue('0.10', [{ method: 'cash', amount: '0.40' }])).toBe('0.30');
    expect(changeDue('50.00', [{ method: 'cash', amount: '100.00' }])).toBe('50.00');
    expect(changeDue('50.00', [{ method: 'cash', amount: '50.00' }])).toBe('0.00');
  });

  it('shows no change on a card payment', () => {
    const text = renderReceipt(receipt([{ method: 'mada', amount: '50.00' }]));
    expect(text).toContain('Mada');
    expect(text).not.toContain('Change');
  });

  it('shows every tender in a split payment', () => {
    const text = renderReceipt(
      receipt([
        { method: 'cash', amount: '20.00' },
        { method: 'mada', amount: '30.00' },
      ]),
    );
    expect(text).toContain('Cash');
    expect(text).toContain('Mada');
  });

  it('multiplies the line out correctly', () => {
    const r = receipt([{ method: 'cash', amount: '50.00' }]);
    expect(r.lines[0]?.lineTotal).toBe('50.00');
  });

  it('says when the sale has not reached the server', () => {
    expect(renderReceipt(receipt([{ method: 'cash', amount: '50.00' }], true)))
      .toContain('Recorded on this terminal.');
    expect(renderReceipt(receipt([{ method: 'cash', amount: '50.00' }], false)))
      .not.toContain('Recorded on this terminal.');
  });

  it('fits the roll', () => {
    const text = renderReceipt(receipt([{ method: 'cash', amount: '50.00' }]));
    for (const row of text.split('\n')) {
      expect(row.length).toBeLessThanOrEqual(42);
    }
  });
});

describe('returns', () => {
  const lines: ReturnableLine[] = [
    {
      line_id: 'l1',
      line_no: 1,
      description: 'Abaya · M · Black',
      qty_sold: '2',
      qty_returned: '0',
      qty_returnable: '2',
      unit_price: '25.00',
      tax_treatment: 'standard',
      tax_rate: '0.15',
      net_returnable: '43.48',
      tax_returnable: '6.52',
      gross_returnable: '50.00',
    },
    {
      line_id: 'l2',
      line_no: 2,
      description: 'Scarf',
      qty_sold: '1',
      qty_returned: '1',
      qty_returnable: '0',
      unit_price: '10.00',
      tax_treatment: 'standard',
      tax_rate: '0.15',
      net_returnable: '0.00',
      tax_returnable: '0.00',
      gross_returnable: '0.00',
    },
  ];

  it('refunds proportionally to the quantity taken back', () => {
    const half: ReturnSelection[] = [{ lineId: 'l1', qty: '1' }];
    expect(refundTotal(lines, half)).toBe('25.00');
  });

  it('refunds the line amount, not quantity times unit price', () => {
    // An invoice-level discount was allocated across the lines, so the two
    // differ — and using the unit price would refund more than the customer
    // paid, reliably in the shop's disfavour.
    const discounted: ReturnableLine[] = [
      { ...lines[0]!, gross_returnable: '45.00' },
    ];
    expect(refundTotal(discounted, [{ lineId: 'l1', qty: '2' }])).toBe('45.00');
    expect(refundTotal(discounted, [{ lineId: 'l1', qty: '1' }])).toBe('22.50');
  });

  it('gives nothing back on a line already fully returned', () => {
    expect(refundTotal(lines, [{ lineId: 'l2', qty: '1' }])).toBe('0.00');
  });

  it('catches an attempt to return more than was sold', () => {
    expect(overReturned(lines, [{ lineId: 'l1', qty: '3' }])).toHaveLength(1);
    expect(overReturned(lines, [{ lineId: 'l1', qty: '2' }])).toHaveLength(0);
  });

  it('catches a second return of a line already given back', () => {
    // The failure this whole flow exists to prevent: refunding the same jacket
    // twice. The server is the authority, but the till must not even offer it.
    expect(overReturned(lines, [{ lineId: 'l2', qty: '1' }])).toHaveLength(1);
  });

  it('rejects a line the invoice does not have', () => {
    expect(overReturned(lines, [{ lineId: 'nope', qty: '1' }])).toHaveLength(1);
  });

  it('rejects a negative or unparseable quantity', () => {
    expect(overReturned(lines, [{ lineId: 'l1', qty: '-1' }])).toHaveLength(1);
    expect(overReturned(lines, [{ lineId: 'l1', qty: 'two' }])).toHaveLength(1);
  });
});

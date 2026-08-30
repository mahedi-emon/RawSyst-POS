// The icon set, drawn rather than downloaded.
//
// # Why these are hand-written paths and not a library
//
// An icon library is a dependency that ships several hundred glyphs to load
// eleven, and every one of them is somebody else's drawing at somebody else's
// optical weight. This product needs about twenty, they need to sit on one
// stroke weight beside each other in a navigation rail, and they need to be
// legible at 18px on a terminal under showroom lighting. Twenty paths is less
// code than the wrapper around a library would be.
//
// # They are decoration, and the markup says so
//
// Every icon here is `aria-hidden`. An icon in this product NEVER carries
// meaning on its own — the navigation rail sets its labels beside them, a
// button says what it does in words, and a status carries a word as well as a
// tint (design system §3: colour is never the only signal). A screen reader
// that announced "chart, arrow, box" would be reading the decoration and
// skipping the interface.
//
// # One stroke, one grid
//
// 24-unit grid, 1.7 stroke, round caps and joins, no fills. Mixing a filled
// icon into a stroked set is the fastest way to make a rail look assembled
// from clip art.

export type IconName =
  | 'dashboard'
  | 'buying'
  | 'inventory'
  | 'stock'
  | 'accounting'
  | 'assets'
  | 'customers'
  | 'expenses'
  | 'settlement'
  | 'devices'
  | 'people'
  | 'einvoicing'
  | 'setup'
  | 'branding'
  | 'counter'
  | 'returns'
  | 'shift'
  | 'menu'
  | 'close'
  | 'chevron'
  | 'search'
  | 'globe'
  | 'signout'
  | 'sun'
  | 'moon'
  | 'plus'
  | 'check'
  | 'alert';

const PATHS: Record<IconName, string> = {
  // A dashboard is panels, not a pie chart: the screen it opens is four tiles.
  dashboard: 'M4 4h7v7H4zM13 4h7v4h-7zM13 10h7v10h-7zM4 13h7v7H4z',
  // Buying is a delivery: a box with its lid seam.
  buying: 'M3 8l9-5 9 5v8l-9 5-9-5zM3 8l9 5 9-5M12 13v8',
  // Inventory is the same box, stacked — what is held rather than what arrived.
  inventory: 'M4 7h7v6H4zM13 4h7v9h-7zM8 13h8v7H8z',
  // Stock is boxes on a shelf, with the shelf drawn. The catalogue glyph
  // beside it is the same boxes WITHOUT one — products the shop sells,
  // against how many are in the building.
  stock: 'M3 20h18M6 20v-5h5v5M13 20v-8h5v8M6 11h5M6 7h5M8.5 4h.01',
  // A ledger: a bound book with a rule down the middle, which is what
  // double entry looks like on paper and has for six hundred years.
  accounting: 'M5 4h14v16H5zM12 4v16M8 9h1M8 13h1M15 9h1M15 13h1',
  // A van: the asset every shop in this product's market owns and the
  // one everybody pictures when the word is used.
  assets: 'M3 17V8h11v9M14 11h4l3 3v3h-7M7.5 17a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0M19 17a1.5 1.5 0 1 1-3 0 1.5 1.5 0 0 1 3 0',
  customers: 'M16 19v-1a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v1M9.5 6.5a3 3 0 1 1-6 0 3 3 0 0 1 6 0M17 11l2 2 4-4',
  // Expenses is money leaving: a note with an arrow out of it.
  expenses: 'M3 7h13v8H3zM9.5 9.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3M18 11h4M20 9l2 2-2 2',
  // Settlement is two halves being matched.
  settlement: 'M4 8h10l-3-3M20 16H10l3 3M4 8v3M20 16v-3',
  // Staff: two figures, the second half behind the first. Deliberately not the
  // `customers` glyph — a shop's own people and the people it sells to are
  // different lists and must not look like the same one.
  people: 'M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7M2 20a7 7 0 0 1 14 0M16.5 11.5a3 3 0 1 0 0-6M18 20h4a6 6 0 0 0-4.5-5.8',
  devices: 'M5 3h14v13H5zM9 20h6M12 16v4',
  // E-invoicing is a document that has been signed.
  einvoicing: 'M6 3h8l4 4v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM14 3v4h4M8 13l2 2 4-4',
  setup: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-1.8-.3 1.6 1.6 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1A1.6 1.6 0 0 0 9 19.4a1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0 .3-1.8 1.6 1.6 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1A1.6 1.6 0 0 0 4.6 9a1.6 1.6 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.6 1.6 0 0 0 1.8.3H9a1.6 1.6 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.6 1.6 0 0 0 1 1.5 1.6 1.6 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0-.3 1.8V9a1.6 1.6 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.6 1.6 0 0 0-1.5 1z',
  branding: 'M12 3l2.6 5.6 6 .8-4.4 4.3 1.1 6.1L12 17l-5.3 2.8 1.1-6.1L3.4 9.4l6-.8z',
  counter: 'M3 6h18l-1.5 9H4.5zM7 20a1 1 0 1 0 0-2 1 1 0 0 0 0 2M17 20a1 1 0 1 0 0-2 1 1 0 0 0 0 2M3 6L2 3',
  returns: 'M4 10h11a5 5 0 0 1 0 10h-4M4 10l4-4M4 10l4 4',
  shift: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18M12 7v5l3 2',
  menu: 'M4 7h16M4 12h16M4 17h16',
  close: 'M6 6l12 12M18 6L6 18',
  chevron: 'M9 6l6 6-6 6',
  search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14M20 20l-4-4',
  globe: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18M3.5 9h17M3.5 15h17M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18',
  signout: 'M15 17l5-5-5-5M20 12H9M11 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h5',
  sun: 'M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
  moon: 'M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5',
  plus: 'M12 5v14M5 12h14',
  check: 'M4 12.5l5 5L20 6.5',
  alert: 'M12 8v5M12 17h.01M10.3 3.9L2.4 17.5A2 2 0 0 0 4.1 20.5h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
};

export function Icon({
  name,
  size = 18,
  className,
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      // Decoration. Whatever this sits beside says what it means.
      aria-hidden="true"
      focusable="false"
    >
      <path d={PATHS[name]} />
    </svg>
  );
}

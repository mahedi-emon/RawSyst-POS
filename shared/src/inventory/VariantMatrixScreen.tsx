// Inventory & Variant Matrix, UI spec §4 — "the fashion/RMG differentiator".
//
// The argument the screen exists to make, in the spec's own words: a standard
// POS shows "Executive Abaya: 126 in stock" and hides that Black XL is out
// while Maroon XXL has not moved in three months. One number, one grid, and the
// difference between them is the whole feature.
//
// # Colour is never the only signal
//
// Every cell that is not normal carries a WORD as well as a tint, and a fuller
// sentence for a screen reader. Roughly one man in twelve has a colour vision
// deficiency and a till is used by whoever is on shift; a grid that said
// "amber means reorder" would be unreadable to one person on the rota and
// unreadable to everybody in bad light.
//
// # Two dimensions, chosen rather than assumed
//
// Blueprint B2 allows unlimited custom attributes, so a product may grid on
// colour x size, fabric x size, or something nobody has thought of. The axes
// are pickers, and anything left over is named under the grid rather than
// silently folded into a cell that would then show one of two variants.

import { useCallback, useEffect, useMemo, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import {
  fetchMatrix,
  listProducts,
  type MatrixCell,
  type Product,
} from '../api/catalog';
import {
  availableAxes,
  buildGrid,
  cellKey,
  DEAD_AFTER_DAYS,
  readCell,
  summarise,
  trimQty,
} from './matrix';

type Load =
  | { state: 'loading' }
  | { state: 'ready'; cells: MatrixCell[] }
  | { state: 'denied' }
  | { state: 'failed'; message: string; offline: boolean };

export function VariantMatrixScreen({ companyId }: { companyId: string }) {
  const { client } = useAuth();

  const [products, setProducts] = useState<Product[] | null>(null);
  const [productId, setProductId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [rowAxis, setRowAxis] = useState<string | null>(null);
  const [colAxis, setColAxis] = useState<string | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  // The product list. Searched server-side rather than filtered here: a shop
  // with forty thousand SKUs is the case this product is for, and shipping all
  // of them to filter in the browser would be the slowest screen in the app.
  const loadProducts = useCallback(
    async (term: string) => {
      setProblem(null);
      try {
        const found = await listProducts(client, companyId, { search: term });
        setProducts(found);
        setProductId((current) =>
          current && found.some((p) => p.id === current) ? current : (found[0]?.id ?? null),
        );
      } catch (err) {
        setProducts([]);
        setProblem(explain(err));
      }
    },
    [client, companyId],
  );

  useEffect(() => {
    void loadProducts('');
  }, [loadProducts]);

  useEffect(() => {
    if (!productId) {
      setLoad({ state: 'ready', cells: [] });
      return;
    }
    let cancelled = false;
    setLoad({ state: 'loading' });

    fetchMatrix(client, productId)
      .then((cells) => {
        if (!cancelled) setLoad({ state: 'ready', cells });
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof RequestFailed && err.status === 403) {
          setLoad({ state: 'denied' });
          return;
        }
        setLoad({
          state: 'failed',
          message: explain(err),
          offline: err instanceof Offline,
        });
      });

    return () => {
      cancelled = true;
    };
  }, [client, productId]);

  const cells = load.state === 'ready' ? load.cells : [];
  const axes = useMemo(() => availableAxes(cells), [cells]);

  // The two most-used attributes, unless somebody has chosen otherwise. Reset
  // when the product changes, because the last product's axes may not exist on
  // this one.
  useEffect(() => {
    setRowAxis((current) => (current && axes.includes(current) ? current : (axes[0] ?? null)));
    setColAxis((current) => (current && axes.includes(current) ? current : (axes[1] ?? null)));
  }, [axes]);

  const grid = useMemo(
    () => (rowAxis && colAxis ? buildGrid(cells, rowAxis, colAxis) : null),
    [cells, rowAxis, colAxis],
  );
  const totals = useMemo(() => summarise(cells), [cells]);
  const product = products?.find((p) => p.id === productId) ?? null;

  return (
    <main className="matrix">
      <header className="matrix__head">
        <div>
          <h1 className="ds-h1">Inventory</h1>
          <p className="ds-body-sm ds-muted">
            What you hold, broken down the way it actually sells.
          </p>
        </div>
      </header>

      <div className="ds-panel">
        <div className="ds-panel__body matrix__picker">
          <label className="matrix__search">
            <span className="ds-caption">Find a product</span>
            <input
              className="field__input"
              type="search"
              value={search}
              placeholder="Name or SKU"
              onChange={(e) => {
                setSearch(e.target.value);
                void loadProducts(e.target.value);
              }}
            />
          </label>

          <label className="matrix__select">
            <span className="ds-caption">Product</span>
            <select
              className="field__input"
              value={productId ?? ''}
              disabled={!products || products.length === 0}
              onChange={(e) => setProductId(e.target.value || null)}
            >
              {(products ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name} · {p.sku}
                </option>
              ))}
            </select>
          </label>

          {grid && axes.length > 1 && (
            <>
              <label className="matrix__select">
                <span className="ds-caption">Rows</span>
                <select
                  className="field__input"
                  value={rowAxis ?? ''}
                  onChange={(e) => setRowAxis(e.target.value)}
                >
                  {axes.map((a) => (
                    <option key={a} value={a} disabled={a === colAxis}>
                      {a}
                    </option>
                  ))}
                </select>
              </label>
              <label className="matrix__select">
                <span className="ds-caption">Columns</span>
                <select
                  className="field__input"
                  value={colAxis ?? ''}
                  onChange={(e) => setColAxis(e.target.value)}
                >
                  {axes.map((a) => (
                    <option key={a} value={a} disabled={a === rowAxis}>
                      {a}
                    </option>
                  ))}
                </select>
              </label>
            </>
          )}
        </div>
      </div>

      {problem && (
        <p className="matrix__problem" role="alert">
          {problem}
        </p>
      )}

      <Body
        load={load}
        product={product}
        grid={grid}
        totals={totals}
        productCount={products?.length ?? null}
      />
    </main>
  );
}

function Body({
  load,
  product,
  grid,
  totals,
  productCount,
}: {
  load: Load;
  product: Product | null;
  grid: ReturnType<typeof buildGrid> | null;
  totals: ReturnType<typeof summarise>;
  productCount: number | null;
}) {
  if (load.state === 'loading') {
    return (
      <div className="ds-panel">
        <div className="ds-panel__body" aria-busy="true">
          <div className="ds-skeleton" style={{ blockSize: 260 }} />
        </div>
      </div>
    );
  }

  if (load.state === 'denied') {
    return (
      <Panel>
        <p className="ds-state__title">This account cannot see the catalogue</p>
        <p className="ds-state__body">
          Reading stock needs permission to view the catalogue. An owner can
          change that under Settings &gt; People.
        </p>
      </Panel>
    );
  }

  if (load.state === 'failed') {
    return (
      <Panel>
        <p className="ds-state__title">
          {load.offline ? 'Stock could not be reached' : 'Stock could not be read'}
        </p>
        <p className="ds-state__body">{load.message}</p>
      </Panel>
    );
  }

  if (productCount === 0) {
    return (
      <Panel>
        <p className="ds-state__title">No products yet</p>
        <p className="ds-state__body">
          Add a product and generate its variants, and the grid will show what
          you hold of each.
        </p>
      </Panel>
    );
  }

  if (!grid || grid.rows.values.length === 0) {
    return (
      <Panel>
        <p className="ds-state__title">
          {product ? `${product.name} has no variants yet` : 'Nothing to show'}
        </p>
        <p className="ds-state__body">
          A grid needs at least one attribute — a size, a colour — on each
          variant. Generate the matrix for this product and its cells will
          appear here.
        </p>
      </Panel>
    );
  }

  return (
    <>
      <div className="ds-panel">
        {/* Wide grids scroll inside their own container; the page never scrolls
            sideways. Spec §4 says so explicitly, and a shop with eight sizes
            and twelve colours is the ordinary case rather than the extreme. */}
        <div className="ds-panel__body ds-scroll-x">
          <table className="matrix__grid">
            <caption className="ds-visually-hidden">
              {product?.name}: stock by {grid.rows.name} and {grid.columns.name}
            </caption>
            <thead>
              <tr>
                <th scope="col" className="matrix__corner">
                  {grid.rows.name} \ {grid.columns.name}
                </th>
                {grid.columns.values.map((c) => (
                  <th scope="col" key={c} className="matrix__colhead">
                    {c}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {grid.rows.values.map((row) => (
                <tr key={row}>
                  <th scope="row" className="matrix__rowhead">
                    {row}
                  </th>
                  {grid.columns.values.map((col) => (
                    <Cell
                      key={col}
                      cell={grid.cells.get(cellKey(row, col))}
                      row={row}
                      column={col}
                    />
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {grid.extraAxes.length > 0 && (
        // A grid shows two dimensions. Saying which ones it is NOT showing is
        // the difference between a cell that means one variant and a cell that
        // quietly means several.
        <p className="matrix__note" role="note">
          These variants also differ by <strong>{grid.extraAxes.join(', ')}</strong>.
          A cell holding more than one of those shows only the first — pick it as
          an axis to separate them.
        </p>
      )}

      {/* Spec §4: the copy under the grid states the point plainly. */}
      <section className="ds-panel matrix__summary" aria-label="What the total hides">
        <div className="ds-panel__body">
          <p className="matrix__total">
            <strong className="num">{totals.total}</strong> in stock across{' '}
            {totals.variants} variant{totals.variants === 1 ? '' : 's'}.
          </p>
          {totals.out + totals.low + totals.dead > 0 ? (
            <p className="ds-body-sm">
              That one number hides{' '}
              {[
                totals.out > 0 ? `${totals.out} out of stock` : null,
                totals.low > 0 ? `${totals.low} at or below reorder` : null,
                totals.dead > 0 ? `${totals.dead} unsold in ${DEAD_AFTER_DAYS} days` : null,
              ]
                .filter(Boolean)
                .join(', ')}
              . A till that showed only the total would let you reorder the wrong
              things.
            </p>
          ) : (
            <p className="ds-body-sm ds-muted">
              Nothing is out, low or sitting unsold. The total is the whole story
              today.
            </p>
          )}
        </div>
      </section>
    </>
  );
}

/** One square. The number, and — when the cell is not ordinary — a word.
 *
 * The word is not decoration for the tint; it is the signal, and the tint is
 * the decoration. That order matters on a screen used in showroom lighting by
 * whoever is on shift. */
function Cell({
  cell,
  row,
  column,
}: {
  cell: MatrixCell | undefined;
  row: string;
  column: string;
}) {
  const reading = readCell(cell);

  return (
    <td className={`matrix__cell matrix__cell--${reading.state}`}>
      {cell ? (
        <>
          <span className="matrix__qty num">{trimQty(cell.on_hand)}</span>
          {reading.label && <span className="matrix__label">{reading.label}</span>}
          <span className="ds-visually-hidden">
            {row} {column}: {reading.description}
          </span>
        </>
      ) : (
        <>
          <span className="matrix__none" aria-hidden="true">
            &middot;
          </span>
          <span className="ds-visually-hidden">
            {row} {column}: not stocked
          </span>
        </>
      )}
    </td>
  );
}

function Panel({ children }: { children: React.ReactNode }) {
  return (
    <div className="ds-panel">
      <div className="ds-state">{children}</div>
    </div>
  );
}

function explain(err: unknown): string {
  if (err instanceof Offline) {
    return (
      'Stock levels are read from the server and cannot be shown while the ' +
      'connection is down. What you last saw may be out of date.'
    );
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return 'This login is not allowed to view the catalogue.';
    return err.message;
  }
  return err instanceof Error ? err.message : 'Something went wrong.';
}

// A batch the shop made rather than bought.
//
// The unit cost is the point. A shirt made in-house has no supplier invoice to
// read a cost off, so the batch IS the cost record: components out at what they
// cost, labour and packaging in, finished units into stock at the total.

export interface ProductionUsage {
  variant_id: string;
  variant?: string;
  qty: string;
  /** What that quantity cost coming out of stock, not what it sells for. */
  cost: string;
}

export interface ProductionBatch {
  id: string;
  batch_no: string;
  variant_id: string;
  variant?: string;
  qty_produced: string;
  material_cost: string;
  labour_cost: string;
  packaging_cost: string;
  total_cost: string;
  /** Computed by the route, never typed: total divided by what was made. */
  unit_cost: string;
  currency: string;
  /** The account labour and packaging were paid from. */
  paid_from: string;
  produced_on: string;
  note?: string;
  inputs?: ProductionUsage[];
}

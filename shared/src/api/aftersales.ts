// Delivery, serial numbers and warranty, repairs, and instalments
// (blueprint B13, B14, B15).
//
// # Availability is three numbers, not one
//
// `on_hand` is what is on the shelf, `reserved` is what another channel has
// already promised, and `available_to_sell` is the only one a second channel
// may act on. Showing one figure is how two channels sell the last unit — the
// thing B13's reservation exists to prevent — so the type carries all three
// and the screens show all three.

import type { Client } from './client';

/** B13's pipeline, exactly. */
export type DeliveryStatus =
  | 'pending'
  | 'assigned'
  | 'picked_up'
  | 'out_for_delivery'
  | 'delivered'
  | 'failed'
  | 'returned';

export interface DeliveryEvent {
  status: DeliveryStatus;
  note?: string;
  recorded_by?: string;
  recorded_at: string;
}

export interface Delivery {
  id: string;
  delivery_no: string;
  order_id: string;
  order_no?: string;
  status: DeliveryStatus;
  customer?: string;

  driver_id?: string;
  driver_name?: string;

  address: string;
  phone?: string;
  fee: string;

  is_cod: boolean;
  cod_amount: string;
  /** The currency the fee and the collection figure are in. */
  currency: string;
  cod_collected_at?: string;

  assigned_at?: string;
  picked_up_at?: string;
  delivered_at?: string;
  failure_reason?: string;
  attempt_count: number;

  note?: string;
  created_at: string;
  events?: DeliveryEvent[];
}

export interface Availability {
  variant_id: string;
  on_hand: string;
  reserved: string;
  available_to_sell: string;
}

export type SerialStatus =
  | 'in_stock'
  | 'reserved'
  | 'sold'
  | 'returned'
  | 'in_repair'
  | 'scrapped';

export interface Serial {
  id: string;
  serial_no: string;
  variant_id: string;
  sku?: string;
  product?: string;
  status: SerialStatus;

  supplier?: string;
  customer?: string;
  invoice_id?: string;

  sold_at?: string;
  warranty_until?: string;
  /** Derived from the date by the server, never stored — a stored flag would
   *  be wrong every morning until a job ran. */
  under_warranty: boolean;
  note?: string;
}

export type ServiceStatus =
  | 'received'
  | 'inspecting'
  | 'awaiting_parts'
  | 'repaired'
  | 'irreparable'
  | 'replaced'
  | 'delivered'
  | 'cancelled';

export interface ServicePart {
  id: string;
  variant_id: string;
  sku?: string;
  qty: string;
  unit_cost: string;
  issued_at: string;
}

export interface ServiceJob {
  id: string;
  job_no: string;
  kind: 'warranty' | 'paid' | 'goodwill';
  status: ServiceStatus;

  customer_id?: string;
  customer?: string;
  serial_id?: string;
  serial_no?: string;
  variant_id?: string;
  product?: string;

  fault_reported: string;
  diagnosis?: string;
  work_done?: string;

  parts_cost: string;
  labour_cost: string;
  charged: string;
  /** The currency the three cost figures are in. */
  currency: string;

  promised_on?: string;
  received_at: string;
  closed_at?: string;

  parts?: ServicePart[];
}

/** B14: Paid / Unpaid / Overdue / Partial, plus waived. Computed by the
 *  database from the money and the date, so it cannot go stale overnight. */
export type DueState = 'paid' | 'unpaid' | 'overdue' | 'partial' | 'waived';

export interface InstallmentDue {
  id: string;
  seq: number;
  due_on: string;
  amount: string;
  paid: string;
  waived: string;
  late_fee: string;
  state: DueState;
}

export interface InstallmentPlan {
  id: string;
  plan_no: string;
  status: 'active' | 'settled' | 'defaulted' | 'cancelled';
  customer_id: string;
  customer?: string;
  invoice_id: string;

  principal: string;
  down_payment: string;
  financed: string;
  markup_rate: string;
  markup_amount: string;
  tenure_months: number;
  installment_amount: string;

  late_fee_flat: string;
  late_fee_rate: string;
  grace_days: number;

  currency: string;
  starts_on: string;

  guarantor_name?: string;
  guarantor_phone?: string;

  outstanding: string;
  schedule?: InstallmentDue[];
}

/** What a plan WOULD look like. Nobody is committed by asking. */
export interface QuotedPlan {
  principal: string;
  down_payment: string;
  financed: string;
  markup_amount: string;
  total_payable: string;
  tenure_months: number;
  installment_amount: string;
  /** The last instalment takes the remainder, so the schedule adds back to the
   *  total rather than falling a hallala short. */
  final_payment: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

// --- Delivery ------------------------------------------------------------

export function listDeliveries(
  client: Client,
  companyId: string,
  q: { status?: string } = {},
): Promise<{ data: Delivery[] }> {
  return client.send<{ data: Delivery[] }>(
    'GET',
    scoped('/api/v1/deliveries' + query(q), companyId),
  );
}

export function readDelivery(
  client: Client,
  companyId: string,
  id: string,
): Promise<Delivery> {
  return client.send<Delivery>(
    'GET',
    scoped(`/api/v1/deliveries/${id}`, companyId),
  );
}

export function bookDelivery(
  client: Client,
  companyId: string,
  body: {
    order_id: string;
    address: string;
    phone?: string;
    fee?: string;
    is_cod?: boolean;
    cod_amount?: string;
    driver_id?: string;
    note?: string;
  },
): Promise<Delivery> {
  return client.send<Delivery>(
    'POST',
    scoped('/api/v1/deliveries', companyId),
    body,
  );
}

export function advanceDelivery(
  client: Client,
  companyId: string,
  id: string,
  body: {
    status: DeliveryStatus;
    note?: string;
    driver_id?: string;
    collected_cod?: boolean;
  },
): Promise<Delivery> {
  return client.send<Delivery>(
    'POST',
    scoped(`/api/v1/deliveries/${id}/advance`, companyId),
    body,
  );
}

export function stockAvailability(
  client: Client,
  companyId: string,
  variantId: string,
  warehouseId: string,
): Promise<Availability> {
  return client.send<Availability>(
    'GET',
    scoped(
      `/api/v1/stock/availability?variant_id=${variantId}&warehouse_id=${warehouseId}`,
      companyId,
    ),
  );
}

// --- Serials -------------------------------------------------------------

export function listSerials(
  client: Client,
  companyId: string,
  q: { status?: string; variant_id?: string } = {},
): Promise<{ data: Serial[] }> {
  return client.send<{ data: Serial[] }>(
    'GET',
    scoped('/api/v1/serials' + query(q), companyId),
  );
}

export function lookupSerial(
  client: Client,
  companyId: string,
  serialNo: string,
): Promise<Serial> {
  return client.send<Serial>(
    'GET',
    scoped(`/api/v1/serials/${encodeURIComponent(serialNo)}`, companyId),
  );
}

export function receiveSerials(
  client: Client,
  companyId: string,
  body: {
    variant_id: string;
    warehouse_id: string;
    grn_id?: string;
    supplier_id?: string;
    serials: string[];
  },
): Promise<{ data: Serial[] }> {
  return client.send<{ data: Serial[] }>(
    'POST',
    scoped('/api/v1/serials', companyId),
    body,
  );
}

// --- Service jobs --------------------------------------------------------

export function listServiceJobs(
  client: Client,
  companyId: string,
  q: { status?: string } = {},
): Promise<{ data: ServiceJob[] }> {
  return client.send<{ data: ServiceJob[] }>(
    'GET',
    scoped('/api/v1/service-jobs' + query(q), companyId),
  );
}

export function readServiceJob(
  client: Client,
  companyId: string,
  id: string,
): Promise<ServiceJob> {
  return client.send<ServiceJob>(
    'GET',
    scoped(`/api/v1/service-jobs/${id}`, companyId),
  );
}

export function bookInRepair(
  client: Client,
  companyId: string,
  body: {
    customer_id?: string;
    store_id?: string;
    serial_no?: string;
    variant_id?: string;
    kind?: string;
    fault_reported: string;
    promised_on?: string;
  },
): Promise<ServiceJob> {
  return client.send<ServiceJob>(
    'POST',
    scoped('/api/v1/service-jobs', companyId),
    body,
  );
}

export function updateRepair(
  client: Client,
  companyId: string,
  id: string,
  body: {
    status?: string;
    diagnosis?: string;
    work_done?: string;
    labour_cost?: string;
    charged?: string;
    replacement_serial?: string;
  },
): Promise<ServiceJob> {
  return client.send<ServiceJob>(
    'POST',
    scoped(`/api/v1/service-jobs/${id}`, companyId),
    body,
  );
}

export function issueServicePart(
  client: Client,
  companyId: string,
  id: string,
  body: { variant_id: string; warehouse_id: string; qty: string },
): Promise<ServiceJob> {
  return client.send<ServiceJob>(
    'POST',
    scoped(`/api/v1/service-jobs/${id}/parts`, companyId),
    body,
  );
}

// --- Instalments ---------------------------------------------------------

export function quoteInstallments(
  client: Client,
  companyId: string,
  body: {
    principal: string;
    down_payment?: string;
    markup_rate?: string;
    tenure_months: number;
  },
): Promise<QuotedPlan> {
  return client.send<QuotedPlan>(
    'POST',
    scoped('/api/v1/installments/quote', companyId),
    body,
  );
}

export function listPlans(
  client: Client,
  companyId: string,
  q: { status?: string } = {},
): Promise<{ data: InstallmentPlan[] }> {
  return client.send<{ data: InstallmentPlan[] }>(
    'GET',
    scoped('/api/v1/installments' + query(q), companyId),
  );
}

export function readPlan(
  client: Client,
  companyId: string,
  id: string,
): Promise<InstallmentPlan> {
  return client.send<InstallmentPlan>(
    'GET',
    scoped(`/api/v1/installments/${id}`, companyId),
  );
}

export function openPlan(
  client: Client,
  companyId: string,
  body: {
    customer_id: string;
    invoice_id: string;
    down_payment?: string;
    markup_rate?: string;
    tenure_months: number;
    starts_on?: string;
    late_fee_flat?: string;
    late_fee_rate?: string;
    grace_days?: number;
    guarantor_name?: string;
    guarantor_phone?: string;
    guarantor_id_no?: string;
    verification_note?: string;
  },
): Promise<InstallmentPlan> {
  return client.send<InstallmentPlan>(
    'POST',
    scoped('/api/v1/installments', companyId),
    body,
  );
}

export function collectInstallment(
  client: Client,
  companyId: string,
  id: string,
  body: { receipt_id: string; amount: string },
): Promise<InstallmentPlan> {
  return client.send<InstallmentPlan>(
    'POST',
    scoped(`/api/v1/installments/${id}/collect`, companyId),
    body,
  );
}

export function cancelPlan(
  client: Client,
  companyId: string,
  id: string,
  reason: string,
): Promise<void> {
  return client.send<void>(
    'POST',
    scoped(`/api/v1/installments/${id}/cancel`, companyId),
    { reason },
  );
}

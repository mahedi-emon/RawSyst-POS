// Staff, attendance, leave, payroll and end of service (blueprint C5, C6).
//
// # Pay is behind its own permission, and so is this file's shape
//
// `hr.view` reaches the directory; `hr.view_pay` reaches what somebody earns;
// `payroll.run` moves money. The salary fields on `Employee` are optional in
// the type because the server omits them for a caller who may not see pay — a
// screen that treated them as required would render "undefined" where a figure
// nobody is allowed to know should simply not appear.

import type { Client } from './client';

export interface Employee {
  id: string;
  employee_no: string;
  user_id?: string;
  full_name: string;
  name_ar?: string;
  phone?: string;
  email?: string;
  position?: string;
  department?: string;
  store_id?: string;
  store_name?: string;

  national_id?: string;
  iqama_no?: string;
  id_expires_on?: string;
  /** C5 asks for Iqama expiry alerting. Derived by the server so every screen
   *  agrees on what "soon" means. */
  id_expiring_soon: boolean;
  id_expired: boolean;

  gosi_number?: string;
  qiwa_contract_no?: string;
  nationality?: string;
  is_saudi: boolean;

  iban?: string;
  bank_name?: string;

  joined_on: string;
  left_on?: string;
  status: string;

  /** Absent for a caller without `hr.view_pay`. Not zero — absent. */
  basic_salary?: string;
  housing_allowance?: string;
  transport_allowance?: string;
  other_allowance?: string;
  currency?: string;
  commission_eligible: boolean;

  notes?: string;
}

export interface EmployeeBody {
  full_name: string;
  name_ar?: string;
  employee_no?: string;
  phone?: string;
  email?: string;
  position?: string;
  department?: string;
  store_id?: string;
  national_id?: string;
  iqama_no?: string;
  id_expires_on?: string;
  gosi_number?: string;
  qiwa_contract_no?: string;
  nationality?: string;
  is_saudi?: boolean;
  iban?: string;
  bank_name?: string;
  joined_on?: string;
  basic_salary?: string;
  housing_allowance?: string;
  transport_allowance?: string;
  other_allowance?: string;
  commission_eligible?: boolean;
  notes?: string;
}

export interface Attendance {
  id: string;
  employee_id: string;
  employee?: string;
  on_date: string;
  status: string;
  hours_worked: string;
  overtime_hours: string;
  late_minutes: number;
  note?: string;
}

export interface Leave {
  id: string;
  employee_id: string;
  employee?: string;
  kind: string;
  is_paid: boolean;
  starts_on: string;
  ends_on: string;
  days: string;
  status: string;
  reason?: string;
  decision_note?: string;
  decided_by?: string;
}

export interface Advance {
  id: string;
  advance_no: string;
  employee_id: string;
  employee?: string;
  amount: string;
  outstanding: string;
  installments: number;
  currency: string;
  issued_on: string;
  reason?: string;
}

export interface Payslip {
  id: string;
  employee_id: string;
  employee: string;
  basic: string;
  housing: string;
  transport: string;
  other_allowance: string;
  overtime: string;
  commission: string;
  bonus: string;
  gross: string;
  gosi_employee?: string;
  gosi_employer?: string;
  advance_repayment?: string;
  other_deduction?: string;
  deductions?: string;
  net: string;
  currency?: string;
}

export interface PayrollRun {
  id: string;
  run_no: string;
  period: string;
  pay_date?: string;
  status: string;
  currency: string;

  gross_total: string;
  deduction_total: string;
  net_total: string;
  employer_gosi: string;

  /** The run computed everything except social insurance, because no verified
   *  rate exists. Named rather than silently zero: a payroll that quietly left
   *  GOSI out is a payroll that files wrong. */
  gosi_unavailable?: boolean;
  gosi_blocked_reason?: string;

  note?: string;
  payslips?: Payslip[];
}

export interface EOSB {
  employee_id: string;
  employee: string;
  months_of_service: string;
  accrued: string;
  currency: string;
}

export interface CommissionRule {
  id: string;
  name: string;
  is_active: boolean;
  basis: string;
  employee_id?: string;
  store_id?: string;
  rate: string;
  tiers: string;
  effective_from: string;
  effective_to?: string;
}

const scoped = (path: string, companyId: string) =>
  `${path}${path.includes('?') ? '&' : '?'}company_id=${companyId}`;

const query = (q: Record<string, string | boolean | undefined>) => {
  const parts = Object.entries(q)
    .filter(([, v]) => v !== undefined && v !== '' && v !== false)
    .map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`);
  return parts.length ? '?' + parts.join('&') : '';
};

// --- the directory --------------------------------------------------------

export function listEmployees(
  client: Client,
  companyId: string,
  q: { search?: string; include_left?: boolean } = {},
): Promise<{ data: Employee[] }> {
  return client.send('GET', scoped('/api/v1/employees' + query(q), companyId));
}

export function readEmployee(
  client: Client,
  companyId: string,
  id: string,
): Promise<Employee> {
  return client.send('GET', scoped(`/api/v1/employees/${id}`, companyId));
}

export function addEmployee(
  client: Client,
  companyId: string,
  body: EmployeeBody,
): Promise<Employee> {
  return client.send('POST', scoped('/api/v1/employees', companyId), body);
}

export function updateEmployee(
  client: Client,
  companyId: string,
  id: string,
  body: EmployeeBody,
): Promise<Employee> {
  return client.send('PUT', scoped(`/api/v1/employees/${id}`, companyId), body);
}

/** Records that somebody has left. Their end-of-service entitlement is
 *  computed from the leaving date, so this is not a soft delete. */
export function recordLeaving(
  client: Client,
  companyId: string,
  id: string,
  body: { left_on: string; reason?: string },
): Promise<Employee> {
  return client.send(
    'POST',
    scoped(`/api/v1/employees/${id}/leaving`, companyId),
    body,
  );
}

/** C5's Iqama and ID expiry alerting. */
export function expiringIDs(
  client: Client,
  companyId: string,
  days = 60,
): Promise<{ data: Employee[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/employees/expiring' + query({ days: String(days) }), companyId),
  );
}

// --- attendance and leave -------------------------------------------------

export function listAttendance(
  client: Client,
  companyId: string,
  q: { from?: string; to?: string; employee_id?: string } = {},
): Promise<{ data: Attendance[] }> {
  return client.send('GET', scoped('/api/v1/attendance' + query(q), companyId));
}

export function recordAttendance(
  client: Client,
  companyId: string,
  body: {
    employee_id: string;
    on_date: string;
    status: string;
    hours_worked?: string;
    overtime_hours?: string;
    late_minutes?: number;
    note?: string;
  },
): Promise<Attendance> {
  return client.send('POST', scoped('/api/v1/attendance', companyId), body);
}

export function listLeave(
  client: Client,
  companyId: string,
  q: { status?: string; employee_id?: string } = {},
): Promise<{ data: Leave[] }> {
  return client.send('GET', scoped('/api/v1/leave' + query(q), companyId));
}

export function requestLeave(
  client: Client,
  companyId: string,
  body: {
    employee_id: string;
    kind: string;
    starts_on: string;
    ends_on: string;
    reason?: string;
  },
): Promise<Leave> {
  return client.send('POST', scoped('/api/v1/leave', companyId), body);
}

export function decideLeave(
  client: Client,
  companyId: string,
  id: string,
  body: { approve: boolean; note?: string },
): Promise<Leave> {
  return client.send(
    'POST',
    scoped(`/api/v1/leave/${id}/decision`, companyId),
    body,
  );
}

// --- advances -------------------------------------------------------------

export function listAdvances(
  client: Client,
  companyId: string,
  includeSettled = false,
): Promise<{ data: Advance[] }> {
  return client.send(
    'GET',
    scoped('/api/v1/advances' + query({ include_settled: includeSettled }), companyId),
  );
}

export function issueAdvance(
  client: Client,
  companyId: string,
  body: {
    employee_id: string;
    amount: string;
    installments: number;
    reason?: string;
  },
): Promise<Advance> {
  return client.send('POST', scoped('/api/v1/advances', companyId), body);
}

// --- payroll --------------------------------------------------------------

export function listPayrollRuns(
  client: Client,
  companyId: string,
): Promise<{ data: PayrollRun[] }> {
  return client.send('GET', scoped('/api/v1/payroll', companyId));
}

export function readPayrollRun(
  client: Client,
  companyId: string,
  id: string,
): Promise<PayrollRun> {
  return client.send('GET', scoped(`/api/v1/payroll/${id}`, companyId));
}

/** Draws a run for a month. Nothing is paid and nothing posts until it is
 *  approved, which is a separate act by a separate person. */
export function runPayroll(
  client: Client,
  companyId: string,
  body: { period: string; note?: string },
): Promise<PayrollRun> {
  return client.send('POST', scoped('/api/v1/payroll', companyId), body);
}

export function approvePayroll(
  client: Client,
  companyId: string,
  id: string,
): Promise<PayrollRun> {
  return client.send(
    'POST',
    scoped(`/api/v1/payroll/${id}/approve`, companyId),
  );
}

export function payPayroll(
  client: Client,
  companyId: string,
  id: string,
  body: { pay_date: string; account_id?: string },
): Promise<PayrollRun> {
  return client.send('POST', scoped(`/api/v1/payroll/${id}/pay`, companyId), body);
}

/** C6's WPS wage file — the bank's own format, which is what makes salaries
 *  payable in Saudi Arabia at all. */
export function wageFile(
  client: Client,
  companyId: string,
  id: string,
): Promise<{ filename: string; content: string; format?: string }> {
  return client.send(
    'POST',
    scoped(`/api/v1/payroll/${id}/wage-file`, companyId),
  );
}

// --- end of service and commission ----------------------------------------

export function endOfService(
  client: Client,
  companyId: string,
): Promise<{ data: EOSB[] }> {
  return client.send('GET', scoped('/api/v1/eosb', companyId));
}

export function accrueEndOfService(
  client: Client,
  companyId: string,
): Promise<{ accrued: string; employees: number }> {
  return client.send('POST', scoped('/api/v1/eosb/accrue', companyId));
}

export function listCommissionRules(
  client: Client,
  companyId: string,
): Promise<{ data: CommissionRule[] }> {
  return client.send('GET', scoped('/api/v1/commission-rules', companyId));
}

export function saveCommissionRule(
  client: Client,
  companyId: string,
  body: {
    name: string;
    basis: string;
    rate: string;
    employee_id?: string;
    store_id?: string;
    effective_from?: string;
  },
): Promise<CommissionRule> {
  return client.send('POST', scoped('/api/v1/commission-rules', companyId), body);
}

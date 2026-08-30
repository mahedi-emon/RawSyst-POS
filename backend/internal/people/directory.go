// The employee directory, attendance and leave (blueprint C5).
package people

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Employee is somebody the business pays.
type Employee struct {
	ID         uuid.UUID  `json:"id"`
	Number     string     `json:"employee_no"`
	UserID     *uuid.UUID `json:"user_id,omitempty"`
	FullName   string     `json:"full_name"`
	NameAr     string     `json:"name_ar,omitempty"`
	Phone      string     `json:"phone,omitempty"`
	Email      string     `json:"email,omitempty"`
	Position   string     `json:"position,omitempty"`
	Department string     `json:"department,omitempty"`
	StoreID    *uuid.UUID `json:"store_id,omitempty"`
	StoreName  string     `json:"store_name,omitempty"`

	NationalID  string `json:"national_id,omitempty"`
	IqamaNo     string `json:"iqama_no,omitempty"`
	IDExpiresOn string `json:"id_expires_on,omitempty"`
	// IDExpiringSoon is C5's alert, derived from the date rather than stored.
	// A stored flag would be wrong every morning until a job ran, and an Iqama
	// that lapsed unnoticed stops somebody working.
	IDExpiringSoon bool   `json:"id_expiring_soon"`
	IDExpired      bool   `json:"id_expired"`
	GOSINumber     string `json:"gosi_number,omitempty"`
	QiwaContract   string `json:"qiwa_contract_no,omitempty"`
	Nationality    string `json:"nationality,omitempty"`
	IsSaudi        bool   `json:"is_saudi"`

	IBAN     string `json:"iban,omitempty"`
	BankName string `json:"bank_name,omitempty"`

	JoinedOn string `json:"joined_on"`
	LeftOn   string `json:"left_on,omitempty"`
	Status   string `json:"status"`

	// The pay fields are empty for a caller without hr.view_pay. Omitted
	// rather than zeroed, so a screen cannot render "0.00" and have somebody
	// believe it — see the package comment on A6.2.
	Basic              string `json:"basic_salary,omitempty"`
	Housing            string `json:"housing_allowance,omitempty"`
	Transport          string `json:"transport_allowance,omitempty"`
	OtherAllowance     string `json:"other_allowance,omitempty"`
	Currency           string `json:"currency,omitempty"`
	CommissionEligible bool   `json:"commission_eligible"`

	Notes string `json:"notes,omitempty"`
}

// NewEmployee is somebody joining.
type NewEmployee struct {
	UserID     *uuid.UUID
	FullName   string
	NameAr     string
	Phone      string
	Email      string
	Position   string
	Department string
	StoreID    *uuid.UUID

	NationalID   string
	IqamaNo      string
	IDExpiresOn  *time.Time
	GOSINumber   string
	QiwaContract string
	Nationality  string
	IsSaudi      bool

	IBAN     string
	BankName string

	JoinedOn time.Time

	Basic              decimal.Decimal
	Housing            decimal.Decimal
	Transport          decimal.Decimal
	OtherAllowance     decimal.Decimal
	CommissionEligible bool

	Notes string
}

// Hire adds somebody to the directory.
func (s *Service) Hire(
	ctx context.Context, scope Scope, in NewEmployee,
) (Employee, error) {
	if strings.TrimSpace(in.FullName) == "" {
		return Employee{}, errs.Validation("Give the employee a name.").
			WithField("full_name", "As it appears on their contract.")
	}
	if in.JoinedOn.IsZero() {
		return Employee{}, errs.Validation("Say when they joined.").
			WithField("joined_on",
				"Length of service decides the end-of-service benefit.")
	}
	for _, p := range []struct {
		v     decimal.Decimal
		field string
	}{{in.Basic, "basic_salary"}, {in.Housing, "housing_allowance"},
		{in.Transport, "transport_allowance"},
		{in.OtherAllowance, "other_allowance"}} {
		if p.v.IsNegative() {
			return Employee{}, errs.Validation("Pay cannot be negative.").
				WithField(p.field, "Use zero if it does not apply.")
		}
	}

	var out Employee
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		number, e := claimNo(ctx, tx, scope.CompanyID, "employee", "EMP")
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO employee
			  (tenant_id, company_id, employee_no, user_id, full_name, name_ar,
			   phone, email, position, department, store_id, national_id,
			   iqama_no, id_expires_on, gosi_number, qiwa_contract_no,
			   nationality, is_saudi, iban, bank_name, joined_on, status,
			   basic_salary, housing_allowance, transport_allowance,
			   other_allowance, currency, commission_eligible, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,
			        $17,$18,$19,$20,$21,'active',$22,$23,$24,$25,$26,$27,$28,$29)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, number, in.UserID,
			strings.TrimSpace(in.FullName), nullText(in.NameAr),
			nullText(in.Phone), nullText(in.Email), nullText(in.Position),
			nullText(in.Department), in.StoreID, nullText(in.NationalID),
			nullText(in.IqamaNo), in.IDExpiresOn, nullText(in.GOSINumber),
			nullText(in.QiwaContract), nullText(in.Nationality), in.IsSaudi,
			nullText(in.IBAN), nullText(in.BankName), in.JoinedOn,
			in.Basic, in.Housing, in.Transport, in.OtherAllowance, currency,
			in.CommissionEligible, nullText(in.Notes), scope.UserID,
		).Scan(&id); e != nil {
			return db.Translate(e,
				"That employee could not be added. Check the login is not "+
					"already attached to somebody else.")
		}

		read, e := s.readEmployee(ctx, tx, scope, id)
		out = read
		return e
	})
	if err != nil {
		return Employee{}, db.Translate(err, "")
	}
	return out, nil
}

// UpdateEmployee changes a record.
type EmployeeUpdate struct {
	FullName   *string
	NameAr     *string
	Phone      *string
	Email      *string
	Position   *string
	Department *string
	StoreID    *uuid.UUID

	IqamaNo      *string
	IDExpiresOn  *time.Time
	GOSINumber   *string
	QiwaContract *string
	IBAN         *string
	BankName     *string

	Basic          *decimal.Decimal
	Housing        *decimal.Decimal
	Transport      *decimal.Decimal
	OtherAllowance *decimal.Decimal

	CommissionEligible *bool
	Notes              *string
}

// Update amends an employee.
//
// Changing pay needs hr.view_pay as well as hr.manage: somebody who may keep a
// roster current must not be able to give themselves a rise, and the split is
// A6.2's whole point.
func (s *Service) Update(
	ctx context.Context, scope Scope, id uuid.UUID, in EmployeeUpdate,
) (Employee, error) {
	touchesPay := in.Basic != nil || in.Housing != nil ||
		in.Transport != nil || in.OtherAllowance != nil
	if touchesPay && !scope.MaySeePay {
		return Employee{}, errs.New(errs.CodeForbidden,
			"Changing pay needs permission to see pay.")
	}

	var out Employee
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE employee SET
			  full_name = coalesce($3, full_name),
			  name_ar = coalesce($4, name_ar),
			  phone = coalesce($5, phone),
			  email = coalesce($6, email),
			  position = coalesce($7, position),
			  department = coalesce($8, department),
			  store_id = coalesce($9, store_id),
			  iqama_no = coalesce($10, iqama_no),
			  id_expires_on = coalesce($11, id_expires_on),
			  gosi_number = coalesce($12, gosi_number),
			  qiwa_contract_no = coalesce($13, qiwa_contract_no),
			  iban = coalesce($14, iban),
			  bank_name = coalesce($15, bank_name),
			  basic_salary = coalesce($16, basic_salary),
			  housing_allowance = coalesce($17, housing_allowance),
			  transport_allowance = coalesce($18, transport_allowance),
			  other_allowance = coalesce($19, other_allowance),
			  commission_eligible = coalesce($20, commission_eligible),
			  notes = coalesce($21, notes)
			WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, in.FullName, in.NameAr, in.Phone, in.Email,
			in.Position, in.Department, in.StoreID, in.IqamaNo, in.IDExpiresOn,
			in.GOSINumber, in.QiwaContract, in.IBAN, in.BankName,
			in.Basic, in.Housing, in.Transport, in.OtherAllowance,
			in.CommissionEligible, in.Notes)
		if e != nil {
			return db.Translate(e, "That employee could not be updated.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That employee was not found.")
		}

		read, e := s.readEmployee(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Leave records somebody's departure.
//
// The record stays. Their name is on the payslips they were paid and the
// invoices they rang up, and deleting it would break both — the same reason
// staff are suspended rather than removed elsewhere in this product.
func (s *Service) Leave(
	ctx context.Context, scope Scope, id uuid.UUID, on time.Time, reason string,
) (Employee, error) {
	var out Employee
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE employee
			SET status = 'left', left_on = $3,
			    notes = coalesce(notes || ' | ', '') || $4
			WHERE id = $1 AND company_id = $2 AND status <> 'left'`,
			id, scope.CompanyID, on, "Left: "+strings.TrimSpace(reason))
		if e != nil {
			return db.Translate(e,
				"That leaving date is before they joined.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound,
				"That employee was not found, or has already left.")
		}
		read, e := s.readEmployee(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// Directory lists staff.
func (s *Service) Directory(
	ctx context.Context, scope Scope, includeLeavers bool,
) ([]Employee, error) {
	out := []Employee{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, employeeSelect+`
			WHERE e.company_id = $1 AND ($2 OR e.status <> 'left')
			ORDER BY e.full_name`, scope.CompanyID, includeLeavers)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			emp, e := scanEmployee(rows, scope.MaySeePay)
			if e != nil {
				return e
			}
			out = append(out, emp)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ExpiringDocuments is C5's Iqama/ID expiry alert.
//
// Within `days`, or already lapsed. An expatriate whose Iqama has run out
// cannot legally work, so this is an operational alert rather than a report.
func (s *Service) ExpiringDocuments(
	ctx context.Context, scope Scope, days int,
) ([]Employee, error) {
	if days <= 0 {
		days = 60
	}
	out := []Employee{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, employeeSelect+`
			WHERE e.company_id = $1 AND e.status <> 'left'
			  AND e.id_expires_on IS NOT NULL
			  AND e.id_expires_on <= current_date + ($2 || ' days')::interval
			ORDER BY e.id_expires_on`, scope.CompanyID, days)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			emp, e := scanEmployee(rows, scope.MaySeePay)
			if e != nil {
				return e
			}
			out = append(out, emp)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// ReadEmployee returns one person.
func (s *Service) ReadEmployee(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Employee, error) {
	var out Employee
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.readEmployee(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

const employeeSelect = `
	SELECT e.id, e.employee_no, e.user_id, e.full_name,
	       coalesce(e.name_ar, ''), coalesce(e.phone, ''),
	       coalesce(e.email, ''), coalesce(e.position, ''),
	       coalesce(e.department, ''), e.store_id, coalesce(st.name, ''),
	       coalesce(e.national_id, ''), coalesce(e.iqama_no, ''),
	       e.id_expires_on, coalesce(e.gosi_number, ''),
	       coalesce(e.qiwa_contract_no, ''), coalesce(e.nationality, ''),
	       e.is_saudi, coalesce(e.iban, ''), coalesce(e.bank_name, ''),
	       e.joined_on, e.left_on, e.status,
	       e.basic_salary, e.housing_allowance, e.transport_allowance,
	       e.other_allowance, e.currency, e.commission_eligible,
	       coalesce(e.notes, '')
	FROM employee e
	LEFT JOIN store st ON st.id = e.store_id`

func (s *Service) readEmployee(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Employee, error) {
	row := tx.QueryRow(ctx, employeeSelect+`
		WHERE e.id = $1 AND e.company_id = $2`, id, scope.CompanyID)
	out, err := scanEmployee(row, scope.MaySeePay)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, errs.New(errs.CodeNotFound,
			"That employee was not found.")
	}
	return out, err
}

func scanEmployee(row scanner, withPay bool) (Employee, error) {
	var e Employee
	var idExpires, leftOn *time.Time
	var joined time.Time
	var basic, housing, transport, other decimal.Decimal
	var currency string

	if err := row.Scan(&e.ID, &e.Number, &e.UserID, &e.FullName, &e.NameAr,
		&e.Phone, &e.Email, &e.Position, &e.Department, &e.StoreID,
		&e.StoreName, &e.NationalID, &e.IqamaNo, &idExpires, &e.GOSINumber,
		&e.QiwaContract, &e.Nationality, &e.IsSaudi, &e.IBAN, &e.BankName,
		&joined, &leftOn, &e.Status, &basic, &housing, &transport, &other,
		&currency, &e.CommissionEligible, &e.Notes); err != nil {
		return Employee{}, err
	}

	e.JoinedOn = joined.Format("2006-01-02")
	if leftOn != nil {
		e.LeftOn = leftOn.Format("2006-01-02")
	}
	if idExpires != nil {
		e.IDExpiresOn = idExpires.Format("2006-01-02")
		today := todayUTC()
		e.IDExpired = idExpires.Before(today)
		e.IDExpiringSoon = !e.IDExpired &&
			idExpires.Before(today.AddDate(0, 0, 60))
	}

	// A6.2's masking, by omission rather than by zeroing: a screen cannot
	// render "0.00" and have somebody believe the person is unpaid.
	if withPay {
		e.Basic = basic.StringFixed(2)
		e.Housing = housing.StringFixed(2)
		e.Transport = transport.StringFixed(2)
		e.OtherAllowance = other.StringFixed(2)
		e.Currency = currency
	}
	return e, nil
}

func todayUTC() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// --- Attendance ----------------------------------------------------------

// AttendanceDay is one person on one day.
type AttendanceDay struct {
	ID         uuid.UUID `json:"id"`
	EmployeeID uuid.UUID `json:"employee_id"`
	Employee   string    `json:"employee,omitempty"`
	OnDate     string    `json:"on_date"`
	Status     string    `json:"status"`
	Hours      string    `json:"hours_worked"`
	Overtime   string    `json:"overtime_hours"`
	LateMins   int       `json:"late_minutes"`
	Note       string    `json:"note,omitempty"`
}

// NewAttendance is a day being recorded.
type NewAttendance struct {
	EmployeeID uuid.UUID
	OnDate     time.Time
	Status     string
	Hours      decimal.Decimal
	Overtime   decimal.Decimal
	LateMins   int
	Note       string
}

// RecordAttendance writes or corrects a day.
//
// Upsert on (employee, day): one row per person per day, because two would
// double-count the hours the payroll run reads. Correcting a day is the same
// act as recording it, and a shop that types Monday twice means the second one.
func (s *Service) RecordAttendance(
	ctx context.Context, scope Scope, in []NewAttendance,
) (int, error) {
	valid := map[string]bool{
		"present": true, "absent": true, "leave": true,
		"holiday": true, "rest_day": true,
	}
	for i, d := range in {
		if !valid[d.Status] {
			return 0, errs.Newf(errs.CodeInvalidInput,
				"Row %d: %q is not an attendance status.", i+1, d.Status)
		}
		if d.Hours.IsNegative() || d.Overtime.IsNegative() || d.LateMins < 0 {
			return 0, errs.Newf(errs.CodeInvalidInput,
				"Row %d has negative hours.", i+1)
		}
	}

	written := 0
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		for _, d := range in {
			// The employee must belong to this company: row-level security
			// proves the tenant, and a group holding two companies would
			// otherwise let one roster the other's staff.
			var ok bool
			e := tx.QueryRow(ctx,
				`SELECT true FROM employee WHERE id = $1 AND company_id = $2`,
				d.EmployeeID, scope.CompanyID).Scan(&ok)
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"That employee was not found.")
			}
			if e != nil {
				return e
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO attendance
				  (tenant_id, company_id, employee_id, on_date, status,
				   hours_worked, overtime_hours, late_minutes, note,
				   recorded_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (employee_id, on_date) DO UPDATE SET
				  status = excluded.status,
				  hours_worked = excluded.hours_worked,
				  overtime_hours = excluded.overtime_hours,
				  late_minutes = excluded.late_minutes,
				  note = excluded.note,
				  recorded_by = excluded.recorded_by`,
				scope.TenantID, scope.CompanyID, d.EmployeeID, d.OnDate,
				d.Status, d.Hours, d.Overtime, d.LateMins, nullText(d.Note),
				scope.UserID); e != nil {
				return e
			}
			written++
		}
		return nil
	})
	return written, db.Translate(err, "")
}

// Attendance reads a period.
func (s *Service) Attendance(
	ctx context.Context, scope Scope, from, to time.Time,
	employeeID *uuid.UUID,
) ([]AttendanceDay, error) {
	out := []AttendanceDay{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT a.id, a.employee_id, e.full_name, a.on_date, a.status,
			       a.hours_worked, a.overtime_hours, a.late_minutes,
			       coalesce(a.note, '')
			FROM attendance a
			JOIN employee e ON e.id = a.employee_id
			WHERE a.company_id = $1 AND a.on_date BETWEEN $2 AND $3
			  AND ($4::uuid IS NULL OR a.employee_id = $4)
			ORDER BY a.on_date DESC, e.full_name
			LIMIT 2000`, scope.CompanyID, from, to, employeeID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var d AttendanceDay
			var on time.Time
			var hours, ot decimal.Decimal
			if e := rows.Scan(&d.ID, &d.EmployeeID, &d.Employee, &on,
				&d.Status, &hours, &ot, &d.LateMins, &d.Note); e != nil {
				return e
			}
			d.OnDate = on.Format("2006-01-02")
			d.Hours = hours.String()
			d.Overtime = ot.String()
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// --- Leave ---------------------------------------------------------------

// LeaveRequest is time off, asked for or granted.
type LeaveRequest struct {
	ID         uuid.UUID `json:"id"`
	EmployeeID uuid.UUID `json:"employee_id"`
	Employee   string    `json:"employee,omitempty"`
	Kind       string    `json:"kind"`
	IsPaid     bool      `json:"is_paid"`
	StartsOn   string    `json:"starts_on"`
	EndsOn     string    `json:"ends_on"`
	Days       string    `json:"days"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Decision   string    `json:"decision_note,omitempty"`
	DecidedBy  string    `json:"decided_by,omitempty"`
}

// RequestLeave asks for time off.
func (s *Service) RequestLeave(
	ctx context.Context, scope Scope, employeeID uuid.UUID,
	kind string, isPaid bool, from, to time.Time, days decimal.Decimal,
	reason string,
) (LeaveRequest, error) {
	if strings.TrimSpace(kind) == "" {
		return LeaveRequest{}, errs.Validation("Say what kind of leave.").
			WithField("kind", "Annual, sick, unpaid, and so on.")
	}
	if to.Before(from) {
		return LeaveRequest{}, errs.New(errs.CodeInvalidInput,
			"Leave cannot end before it starts.")
	}
	if !days.IsPositive() {
		// Derived when the caller did not say, so a shop entering a simple
		// date range does not have to count.
		days = decimal.NewFromInt(int64(to.Sub(from).Hours()/24) + 1)
	}

	var out LeaveRequest
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var ok bool
		e := tx.QueryRow(ctx,
			`SELECT true FROM employee WHERE id = $1 AND company_id = $2`,
			employeeID, scope.CompanyID).Scan(&ok)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That employee was not found.")
		}
		if e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO leave_request
			  (tenant_id, company_id, employee_id, kind, is_paid, starts_on,
			   ends_on, days, status, reason, requested_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'requested',$9,$10)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, employeeID,
			strings.TrimSpace(kind), isPaid, from, to, days,
			nullText(reason), scope.UserID).Scan(&id); e != nil {
			return e
		}
		read, e := s.readLeave(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// DecideLeave approves or refuses time off.
func (s *Service) DecideLeave(
	ctx context.Context, scope Scope, id uuid.UUID, approve bool, note string,
) (LeaveRequest, error) {
	if !approve && strings.TrimSpace(note) == "" {
		return LeaveRequest{}, errs.Validation(
			"Say why the leave is refused.").
			WithField("decision_note",
				"The person asking has to be able to plan around the answer.")
	}
	status := "approved"
	if !approve {
		status = "rejected"
	}

	var out LeaveRequest
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE leave_request
			SET status = $3, decision_note = $4, decided_by = $5,
			    decided_at = now()
			WHERE id = $1 AND company_id = $2 AND status = 'requested'`,
			id, scope.CompanyID, status, nullText(note), scope.UserID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeConflict,
				"That leave request was not found, or has already been decided.")
		}

		// Approved leave becomes attendance, so the payroll run reads one
		// source. Unpaid leave lands as an absence and is deducted; paid leave
		// is a normal day the person did not have to work.
		if approve {
			if _, e := tx.Exec(ctx, `
				INSERT INTO attendance
				  (tenant_id, company_id, employee_id, on_date, status,
				   hours_worked, note, recorded_by)
				SELECT l.tenant_id, l.company_id, l.employee_id,
				       d::date,
				       CASE WHEN l.is_paid THEN 'leave' ELSE 'absent' END,
				       0, 'Leave: ' || l.kind, $2
				FROM leave_request l,
				     generate_series(l.starts_on, l.ends_on, '1 day') d
				WHERE l.id = $1
				ON CONFLICT (employee_id, on_date) DO UPDATE SET
				  status = excluded.status, note = excluded.note`,
				id, scope.UserID); e != nil {
				return e
			}
		}

		read, e := s.readLeave(ctx, tx, scope.CompanyID, id)
		out = read
		return e
	})
	return out, db.Translate(err, "")
}

// LeaveRequests lists time off.
func (s *Service) LeaveRequests(
	ctx context.Context, scope Scope, status string,
) ([]LeaveRequest, error) {
	out := []LeaveRequest{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, leaveSelect+`
			WHERE l.company_id = $1 AND ($2 = '' OR l.status = $2)
			ORDER BY l.starts_on DESC LIMIT 500`, scope.CompanyID, status)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			lr, e := scanLeave(rows)
			if e != nil {
				return e
			}
			out = append(out, lr)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

const leaveSelect = `
	SELECT l.id, l.employee_id, e.full_name, l.kind, l.is_paid,
	       l.starts_on, l.ends_on, l.days, l.status,
	       coalesce(l.reason, ''), coalesce(l.decision_note, ''),
	       coalesce(u.full_name, '')
	FROM leave_request l
	JOIN employee e ON e.id = l.employee_id
	LEFT JOIN app_user u ON u.id = l.decided_by`

func (s *Service) readLeave(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (LeaveRequest, error) {
	row := tx.QueryRow(ctx, leaveSelect+`
		WHERE l.id = $1 AND l.company_id = $2`, id, companyID)
	out, err := scanLeave(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return LeaveRequest{}, errs.New(errs.CodeNotFound,
			"That leave request was not found.")
	}
	return out, err
}

func scanLeave(row scanner) (LeaveRequest, error) {
	var l LeaveRequest
	var from, to time.Time
	var days decimal.Decimal
	if err := row.Scan(&l.ID, &l.EmployeeID, &l.Employee, &l.Kind, &l.IsPaid,
		&from, &to, &days, &l.Status, &l.Reason, &l.Decision,
		&l.DecidedBy); err != nil {
		return LeaveRequest{}, err
	}
	l.StartsOn = from.Format("2006-01-02")
	l.EndsOn = to.Format("2006-01-02")
	l.Days = days.String()
	return l, nil
}

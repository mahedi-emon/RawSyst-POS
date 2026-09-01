// Package group is multi-company groups and consolidated reporting (F4).
//
// # A group owns no books
//
// Every posting belongs to one company. A group is a set of companies and a way
// to add them up, and consolidation is a query rather than a second ledger. F4
// requires it: each company in a Saudi group is a separate legal entity with its
// own commercial registration, its own VAT number and its own e-invoicing
// sequence, and an entry belonging to "the group" is an entry no company could
// file.
//
// # Elimination removes what somebody marked, and nothing else
//
// F4 asks for inter-company transactions to be "tracked and eliminated in
// consolidation". Inferring them — matching amounts and dates, or a customer
// whose name resembles a sister company — is how a consolidation quietly
// deletes a real third-party sale, and the shop finds out when the figures
// disagree with the bank.
//
// So an entry is inter-company when a person said so. `Eliminated` reports what
// was removed and what it came to, beside the consolidated total, because the
// number an accountant checks is the difference between the sum and the
// consolidation.
//
// # A partly-owned company is reported at the group's share, and says so
//
// A group holding 60% of a subsidiary and reporting 100% of its profit is
// publishing a false figure. Each company's contribution is scaled by the
// recorded ownership, and the response names the percentage used for every
// company — so a reader can see the arithmetic rather than trust it.
//
// This is proportional consolidation, and it is stated rather than assumed.
// Full consolidation with a minority interest line is the other defensible
// treatment, it needs an equity account for the minority and a policy decision
// about control that nobody has made here, and inventing one would be inventing
// an accounting rule.
//
// # Currency
//
// Every company's figures are already in its own base currency in the ledger.
// A group whose companies share a base currency consolidates exactly. A group
// whose companies do not is reported per company, with the presentation
// currency named and the differing companies flagged — because translating a
// subsidiary's balance sheet needs closing rates, average rates and a
// translation reserve, and doing it with a single spot rate would be a wrong
// answer that looks like a right one.
package group

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service carries groups and consolidated statements.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking.
type Scope struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// Group is one set of companies.
type Group struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	NameAr   string    `json:"name_ar,omitempty"`
	Currency string    `json:"presentation_currency"`
	Members  []Member  `json:"members"`
}

// Member is one company in a group.
type Member struct {
	CompanyID uuid.UUID `json:"company_id"`
	Name      string    `json:"name"`
	// The company's own books currency, so a screen can show which members
	// differ from the presentation currency without a second request.
	BaseCurrency string `json:"base_currency"`
	Ownership    string `json:"ownership_pct"`
	IsParent     bool   `json:"is_parent"`
	JoinedOn     string `json:"joined_on"`
	LeftOn       string `json:"left_on,omitempty"`
}

// Groups lists them.
func (s *Service) Groups(ctx context.Context, scope Scope) ([]Group, error) {
	out := []Group{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, name, coalesce(name_ar, ''), presentation_currency
			FROM company_group WHERE tenant_id = $1 ORDER BY name`,
			scope.TenantID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var g Group
			if e := rows.Scan(&g.ID, &g.Name, &g.NameAr,
				&g.Currency); e != nil {
				return e
			}
			out = append(out, g)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		for i := range out {
			members, e := membersOf(ctx, tx, out[i].ID)
			if e != nil {
				return e
			}
			out[i].Members = members
		}
		return nil
	})
	return out, db.Translate(err, "")
}

func membersOf(
	ctx context.Context, tx pgx.Tx, groupID uuid.UUID,
) ([]Member, error) {
	out := []Member{}
	rows, err := tx.Query(ctx, `
		SELECT m.company_id, c.legal_name, c.base_currency, m.ownership_pct,
		       m.is_parent, m.joined_on, m.left_on
		FROM company_group_member m
		JOIN company c ON c.id = m.company_id
		WHERE m.group_id = $1
		ORDER BY m.is_parent DESC, c.legal_name`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		var pct decimal.Decimal
		var joined time.Time
		var left *time.Time
		if e := rows.Scan(&m.CompanyID, &m.Name, &m.BaseCurrency, &pct,
			&m.IsParent, &joined, &left); e != nil {
			return nil, e
		}
		m.Ownership = pct.StringFixed(2)
		m.JoinedOn = joined.Format("2006-01-02")
		if left != nil {
			m.LeftOn = left.Format("2006-01-02")
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveGroup creates or renames a group.
func (s *Service) SaveGroup(
	ctx context.Context, scope Scope, id *uuid.UUID, name, nameAr,
	currency string,
) (Group, error) {
	if strings.TrimSpace(name) == "" {
		return Group{}, errs.New(errs.CodeInvalidInput, "Name the group.")
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return Group{}, errs.New(errs.CodeInvalidInput,
			"Name the currency the group's statements are presented in.")
	}

	var groupID uuid.UUID
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var e error
		if id == nil {
			e = tx.QueryRow(ctx, `
				INSERT INTO company_group (
				  tenant_id, name, name_ar, presentation_currency)
				VALUES ($1,$2,nullif($3,''),$4) RETURNING id`,
				scope.TenantID, strings.TrimSpace(name), nameAr, currency).
				Scan(&groupID)
		} else {
			groupID = *id
			e = tx.QueryRow(ctx, `
				UPDATE company_group
				   SET name = $2, name_ar = nullif($3,''),
				       presentation_currency = $4
				 WHERE id = $1 AND tenant_id = $5
				RETURNING id`,
				groupID, strings.TrimSpace(name), nameAr, currency,
				scope.TenantID).Scan(&groupID)
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That group was not found.")
			}
		}
		if e != nil {
			return db.Translate(e,
				"A group of that name already exists.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "company_group_saved",
			EntityType: "company_group", EntityID: &groupID,
			After: map[string]any{"name": name, "currency": currency},
		})
	})
	if err != nil {
		return Group{}, err
	}
	return s.Group(ctx, scope, groupID)
}

// Group reads one.
func (s *Service) Group(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Group, error) {
	var out Group
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT id, name, coalesce(name_ar, ''), presentation_currency
			FROM company_group WHERE id = $1 AND tenant_id = $2`,
			id, scope.TenantID).Scan(&out.ID, &out.Name, &out.NameAr,
			&out.Currency)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That group was not found.")
		}
		if e != nil {
			return e
		}
		members, e := membersOf(ctx, tx, id)
		out.Members = members
		return e
	})
	return out, db.Translate(err, "")
}

// AddMember puts a company in a group.
func (s *Service) AddMember(
	ctx context.Context, scope Scope, groupID, companyID uuid.UUID,
	ownership string, isParent bool,
) (Group, error) {
	pct := decimal.NewFromInt(100)
	if strings.TrimSpace(ownership) != "" {
		p, err := decimal.NewFromString(strings.TrimSpace(ownership))
		if err != nil || !p.IsPositive() ||
			p.GreaterThan(decimal.NewFromInt(100)) {
			return Group{}, errs.New(errs.CodeInvalidInput,
				"A holding runs from just above nothing to one hundred "+
					"per cent.")
		}
		pct = p
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// The company has to be this tenant's. Row-level security already
		// confines the write, but the check turns a foreign-key violation into
		// a sentence a person can act on.
		var exists bool
		if e := tx.QueryRow(ctx,
			`SELECT true FROM company WHERE id = $1 AND tenant_id = $2`,
			companyID, scope.TenantID).Scan(&exists); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That company was not found.")
			}
			return e
		}

		// One parent per group, enforced by a partial unique index. Clearing
		// the old one first makes "make this the parent" work rather than
		// conflict, which is what somebody pressing it means.
		if isParent {
			if _, e := tx.Exec(ctx, `
				UPDATE company_group_member SET is_parent = false
				 WHERE group_id = $1 AND is_parent`, groupID); e != nil {
				return e
			}
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO company_group_member (
			  group_id, company_id, tenant_id, ownership_pct, is_parent)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (group_id, company_id) DO UPDATE SET
			  ownership_pct = excluded.ownership_pct,
			  is_parent = excluded.is_parent,
			  left_on = NULL`,
			groupID, companyID, scope.TenantID, pct, isParent); e != nil {
			return db.Translate(e,
				"That company already belongs to another group. A company "+
					"in two groups would be consolidated twice.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "company_group_member_added",
			EntityType: "company_group", EntityID: &groupID,
			After: map[string]any{
				"company_id": companyID.String(),
				"ownership":  pct.StringFixed(2), "is_parent": isParent,
			},
		})
	})
	if err != nil {
		return Group{}, db.Translate(err, "")
	}
	return s.Group(ctx, scope, groupID)
}

// RemoveMember takes a company out of a group.
func (s *Service) RemoveMember(
	ctx context.Context, scope Scope, groupID, companyID uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				DELETE FROM company_group_member
				 WHERE group_id = $1 AND company_id = $2 AND tenant_id = $3`,
				groupID, companyID, scope.TenantID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That company is not in that group.")
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "company_group_member_removed",
				EntityType: "company_group", EntityID: &groupID,
				Before: map[string]any{"company_id": companyID.String()},
			})
		}), "")
}

// RemoveGroup deletes a group. The companies and their books are untouched.
func (s *Service) RemoveGroup(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx,
				`DELETE FROM company_group WHERE id = $1 AND tenant_id = $2`,
				id, scope.TenantID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That group was not found.")
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "company_group_removed",
				EntityType: "company_group", EntityID: &id,
			})
		}), "")
}

// ---------------------------------------------------------------------------
// Inter-company marking
// ---------------------------------------------------------------------------

// Intercompany is one marked entry.
type Intercompany struct {
	EntryID      uuid.UUID `json:"entry_id"`
	EntryNo      string    `json:"entry_no"`
	EntryDate    string    `json:"entry_date"`
	Memo         string    `json:"memo,omitempty"`
	CompanyID    uuid.UUID `json:"company_id"`
	Counterparty uuid.UUID `json:"counterparty_id"`
	Kind         string    `json:"kind"`
	Note         string    `json:"note,omitempty"`
	Amount       string    `json:"amount"`
	MarkedBy     string    `json:"marked_by,omitempty"`
}

// MarkIntercompany records that an entry is between two group companies.
func (s *Service) MarkIntercompany(
	ctx context.Context, scope Scope, entryID, counterpartyID uuid.UUID,
	kind, note string,
) error {
	if kind == "" {
		kind = "sale"
	}
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			var companyID uuid.UUID
			e := tx.QueryRow(ctx,
				`SELECT company_id FROM journal_entry WHERE id = $1`,
				entryID).Scan(&companyID)
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That journal entry was not found.")
			}
			if e != nil {
				return e
			}

			// Both companies have to be in the same group, or the mark would
			// eliminate a transaction with a third party that happens to be
			// another of this tenant's companies.
			var together bool
			if e := tx.QueryRow(ctx, `
				SELECT EXISTS (
				  SELECT 1 FROM company_group_member a
				  JOIN company_group_member b ON b.group_id = a.group_id
				  WHERE a.company_id = $1 AND b.company_id = $2)`,
				companyID, counterpartyID).Scan(&together); e != nil {
				return e
			}
			if !together {
				return errs.New(errs.CodeInvalidInput,
					"Those two companies are not in the same group, so an "+
						"entry between them is not eliminated on "+
						"consolidation.")
			}

			if _, e := tx.Exec(ctx, `
				INSERT INTO intercompany_entry (
				  entry_id, tenant_id, company_id, counterparty_id, kind,
				  note, marked_by)
				VALUES ($1,$2,$3,$4,$5,nullif($6,''),$7)
				ON CONFLICT (entry_id) DO UPDATE SET
				  counterparty_id = excluded.counterparty_id,
				  kind = excluded.kind, note = excluded.note,
				  marked_by = excluded.marked_by, marked_at = now()`,
				entryID, scope.TenantID, companyID, counterpartyID, kind,
				note, scope.UserID); e != nil {
				return db.Translate(e, "That entry could not be marked.")
			}

			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "intercompany_marked",
				EntityType: "journal_entry", EntityID: &entryID,
				After: map[string]any{
					"counterparty": counterpartyID.String(), "kind": kind,
				},
			})
		}), "")
}

// UnmarkIntercompany takes the annotation off.
func (s *Service) UnmarkIntercompany(
	ctx context.Context, scope Scope, entryID uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx,
				`DELETE FROM intercompany_entry
				  WHERE entry_id = $1 AND tenant_id = $2`,
				entryID, scope.TenantID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That entry is not marked as inter-company.")
			}
			return audit.Write(ctx, tx, audit.Entry{
				TenantID: &scope.TenantID, ActorID: &scope.UserID,
				ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
				Action:     "intercompany_unmarked",
				EntityType: "journal_entry", EntityID: &entryID,
			})
		}), "")
}

// Intercompanies lists what is marked in a group, for the period.
func (s *Service) Intercompanies(
	ctx context.Context, scope Scope, groupID uuid.UUID, from, to time.Time,
) ([]Intercompany, error) {
	out := []Intercompany{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT i.entry_id, e.entry_no, e.entry_date, coalesce(e.memo, ''),
			       i.company_id, i.counterparty_id, i.kind,
			       coalesce(i.note, ''),
			       coalesce((SELECT sum(l.base_debit) FROM journal_line l
			                  WHERE l.entry_id = e.id), 0),
			       coalesce(u.full_name, '')
			FROM intercompany_entry i
			JOIN journal_entry e ON e.id = i.entry_id
			JOIN company_group_member m ON m.company_id = i.company_id
			LEFT JOIN app_user u ON u.id = i.marked_by
			WHERE m.group_id = $1
			  AND e.entry_date BETWEEN $2::date AND $3::date
			ORDER BY e.entry_date DESC, e.entry_no DESC
			LIMIT 500`, groupID, from, to)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var ic Intercompany
			var date time.Time
			var amount decimal.Decimal
			if e := rows.Scan(&ic.EntryID, &ic.EntryNo, &date, &ic.Memo,
				&ic.CompanyID, &ic.Counterparty, &ic.Kind, &ic.Note,
				&amount, &ic.MarkedBy); e != nil {
				return e
			}
			ic.EntryDate = date.Format("2006-01-02")
			ic.Amount = amount.StringFixed(2)
			out = append(out, ic)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

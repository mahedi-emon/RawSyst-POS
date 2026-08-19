// EGS units — the software units that sign invoices.
//
// An EGS Unit is "the software unit that generates and signs one unique invoice
// sequence" (Technical Guideline V2 §3.5). One unit owns exactly one ICV/PIH
// chain, and every terminal points at the unit that signs for it. 0013 made
// that an entity and stopped there: nothing in the product could create one, so
// a terminal enrolled through the back office had `egs_unit_id IS NULL` and
// sales.resolveTerminal refused every sale it tried to make. This package is
// what a shop uses to create the unit and make that terminal sellable.
//
// # What this package deliberately does not do
//
// It does not onboard. There is no method here that talks to ZATCA, builds a
// CSR, or sets a CSID: the CSID columns are read-only through this service and
// stay at their defaults. The nine CSR fields are CAPTURED and format-checked,
// because a shop can only supply them once and a wrong VAT number is a support
// call at onboarding rather than a silent failure. Turning them into a CSR
// needs the byte-level formats that are still unverified — the same wall
// zatca.DocumentHasher and zatca.UnverifiedSubmitter stand at.
//
// So the honest summary is: this decides which chain a till writes to, and
// records the identity that chain will eventually be certified under.
package egs

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service reads and writes EGS units. Every method runs inside the tenant's
// row-level security context, exactly as devices.Service does; nothing here
// reaches for the platform escape.
type Service struct {
	pool *db.Pool
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and about which company. Mirrors devices.Scope rather
// than inventing a second shape for the same idea.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// The three architectures ZATCA documents. Repeated from the check constraint
// in 0013 so a bad value is refused with a sentence rather than a constraint
// violation, and named as constants so the binding rules below read.
const (
	Centralized  = "centralized_server"
	BranchServer = "branch_server"
	SmartPOS     = "smart_pos"
)

// CSR is the nine fields Technical Guideline V2 §3.3.3 requires. Held as nine
// columns rather than a JSON blob for the reason 0013 gives: a typo in one of
// them is not noticed until onboarding rejects the request.
type CSR struct {
	CommonName             string `json:"common_name"`
	EGSSerialNumber        string `json:"egs_serial_number"`
	OrganizationIdentifier string `json:"organization_identifier"`
	OrganizationUnit       string `json:"organization_unit"`
	OrganizationName       string `json:"organization_name"`
	Country                string `json:"country"`
	InvoiceType            string `json:"invoice_type"`
	Location               string `json:"location"`
	Industry               string `json:"industry"`
}

type Unit struct {
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`

	// Architecture decides where signing happens and therefore where the
	// private key must live. It is fixed at creation: changing it would move
	// the key custody model under a chain that already exists.
	Architecture string `json:"architecture"`

	// Empty for a centralized unit, which serves the whole company.
	StoreID string `json:"store_id,omitempty"`
	Store   string `json:"store,omitempty"`

	CSR CSR `json:"csr"`

	// Read-only. Nothing in this service writes them; they move when onboarding
	// ships against a verified source.
	CSIDStatus    string `json:"csid_status"`
	CSIDSerial    string `json:"csid_serial,omitempty"`
	CSIDIssuedAt  string `json:"csid_issued_at,omitempty"`
	CSIDExpiresAt string `json:"csid_expires_at,omitempty"`

	// Terminals is how many tills sign under this unit. A shop deciding whether
	// a unit is still needed asks this question first.
	Terminals int `json:"terminals"`

	// Invoices is the length of the chain this unit owns. Non-zero makes the
	// unit historical evidence: its architecture and the terminals bound to it
	// stop being freely editable.
	Invoices int `json:"invoices"`

	// CSRComplete is derived, never stored. All nine fields are mandatory at
	// onboarding, so a shop needs to know which units are ready before it
	// starts and which will be refused. It is not a compliance claim: a
	// complete CSR is not an accepted one.
	CSRComplete bool `json:"csr_complete"`
}

type NewUnit struct {
	Label        string
	Architecture string
	StoreID      uuid.UUID
	CSR          CSR
}

// Amendment corrects a unit. Architecture is absent on purpose: it is chosen
// once, because it decides where the signing key lives.
type Amendment struct {
	Label   string
	StoreID uuid.UUID
	CSR     CSR
}

// --- validation ------------------------------------------------------------

// Mirrors of the check constraints in 0013. Duplicated here so the caller gets
// a sentence naming the field instead of a Postgres error naming the
// constraint; the database remains the authority either way.
var (
	vatNumber   = regexp.MustCompile(`^3[0-9]{13}3$`)
	invoiceType = regexp.MustCompile(`^[01][01]00$`)
	tenDigits   = regexp.MustCompile(`^[0-9]{10}$`)
)

func trim(c CSR) CSR {
	return CSR{
		CommonName:             strings.TrimSpace(c.CommonName),
		EGSSerialNumber:        strings.TrimSpace(c.EGSSerialNumber),
		OrganizationIdentifier: strings.TrimSpace(c.OrganizationIdentifier),
		OrganizationUnit:       strings.TrimSpace(c.OrganizationUnit),
		OrganizationName:       strings.TrimSpace(c.OrganizationName),
		Country:                strings.ToLower(strings.TrimSpace(c.Country)),
		InvoiceType:            strings.TrimSpace(c.InvoiceType),
		Location:               strings.TrimSpace(c.Location),
		Industry:               strings.TrimSpace(c.Industry),
	}
}

func complete(c CSR) bool {
	return c.CommonName != "" && c.EGSSerialNumber != "" &&
		c.OrganizationIdentifier != "" && c.OrganizationUnit != "" &&
		c.OrganizationName != "" && c.Country != "" &&
		c.InvoiceType != "" && c.Location != "" && c.Industry != ""
}

// validate checks what can be checked without asserting anything ZATCA has not
// published.
//
// The fields are individually optional, and that is a deliberate choice rather
// than an oversight. All nine are mandatory AT ONBOARDING, which is a later act
// this milestone does not perform; refusing to save a unit until a shop has
// tracked down its industry classification would stop them registering the till
// they need to trade today. What is refused is a field in the WRONG SHAPE,
// because that is the failure ZATCA rejects a CSR for.
func validate(label, architecture string, storeID uuid.UUID, c CSR) error {
	e := errs.New(errs.CodeInvalidInput, "Some of these details need correcting.")

	if label == "" {
		e.WithField("label", "Give this unit a name you will recognise, like \"Main branch\".")
	}

	switch architecture {
	case Centralized:
		if storeID != uuid.Nil {
			e.WithField("store_id",
				"A central unit signs for the whole business, so it is not tied to one branch.")
		}
	case BranchServer, SmartPOS:
		if storeID == uuid.Nil {
			e.WithField("store_id", "Say which branch this unit is in.")
		}
	default:
		e.WithField("architecture", "Choose how this unit signs: centrally, per branch, or on the till itself.")
	}

	if c.OrganizationIdentifier != "" && !vatNumber.MatchString(c.OrganizationIdentifier) {
		e.WithField("csr.organization_identifier",
			"A Saudi VAT number is 15 digits and starts and ends with 3.")
	}
	if c.InvoiceType != "" && !invoiceType.MatchString(c.InvoiceType) {
		e.WithField("csr.invoice_type",
			"Choose which invoices this unit issues: standard, simplified, or both.")
	}
	if c.Country != "" && len(c.Country) != 2 {
		e.WithField("csr.country", "Use the two-letter country code, like SA.")
	}

	// SA.ZATCA.CSR_FIELDS: when the 11th digit of the VAT number is 1 the
	// taxpayer is a VAT group, and the organization unit must carry the
	// 10-digit TIN of the member being onboarded rather than a branch name.
	// Checked here because it is verified, and because getting it wrong is
	// discovered at onboarding rather than now.
	if len(c.OrganizationIdentifier) == 15 && c.OrganizationIdentifier[10] == '1' &&
		c.OrganizationUnit != "" && !tenDigits.MatchString(c.OrganizationUnit) {
		e.WithField("csr.organization_unit",
			"This VAT number belongs to a VAT group, so this field must be the "+
				"10-digit tax number of the member being registered, not a branch name.")
	}

	if len(e.Fields) > 0 {
		return e
	}
	return nil
}

// --- reading ---------------------------------------------------------------

const unitSelect = `
	SELECT u.id, u.label, u.architecture,
	       coalesce(u.store_id::text, ''), coalesce(s.name, ''),
	       coalesce(u.csr_common_name, ''),
	       coalesce(u.csr_egs_serial_number, ''),
	       coalesce(u.csr_organization_identifier, ''),
	       coalesce(u.csr_organization_unit, ''),
	       coalesce(u.csr_organization_name, ''),
	       coalesce(u.csr_country, ''),
	       coalesce(u.csr_invoice_type, ''),
	       coalesce(u.csr_location, ''),
	       coalesce(u.csr_industry, ''),
	       u.csid_status,
	       coalesce(u.csid_serial, ''),
	       coalesce(to_char(u.csid_issued_at,  'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       coalesce(to_char(u.csid_expires_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), ''),
	       (SELECT count(*) FROM device d WHERE d.egs_unit_id = u.id),
	       (SELECT count(*) FROM zatca_invoice z WHERE z.egs_unit_id = u.id)
	FROM egs_unit u
	LEFT JOIN store s ON s.id = u.store_id`

type scanner interface{ Scan(dest ...any) error }

func scanUnit(row scanner) (Unit, error) {
	var u Unit
	if err := row.Scan(&u.ID, &u.Label, &u.Architecture,
		&u.StoreID, &u.Store,
		&u.CSR.CommonName, &u.CSR.EGSSerialNumber, &u.CSR.OrganizationIdentifier,
		&u.CSR.OrganizationUnit, &u.CSR.OrganizationName, &u.CSR.Country,
		&u.CSR.InvoiceType, &u.CSR.Location, &u.CSR.Industry,
		&u.CSIDStatus, &u.CSIDSerial, &u.CSIDIssuedAt, &u.CSIDExpiresAt,
		&u.Terminals, &u.Invoices); err != nil {
		return Unit{}, err
	}
	u.CSRComplete = complete(u.CSR)
	return u, nil
}

func (s *Service) read(ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID) (Unit, error) {
	u, err := scanUnit(tx.QueryRow(ctx, unitSelect+`
		WHERE u.id = $1 AND u.company_id = $2`, id, scope.CompanyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, errs.New(errs.CodeNotFound, "That e-invoicing unit was not found.")
	}
	return u, err
}

func (s *Service) List(ctx context.Context, scope Scope) ([]Unit, error) {
	out := []Unit{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, unitSelect+`
			WHERE u.company_id = $1
			ORDER BY coalesce(s.name, ''), u.label`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			u, e := scanUnit(rows)
			if e != nil {
				return e
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) Read(ctx context.Context, scope Scope, id uuid.UUID) (Unit, error) {
	var out Unit
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		u, e := s.read(ctx, tx, scope, id)
		out = u
		return e
	})
	return out, err
}

// --- writing ---------------------------------------------------------------

func (s *Service) Create(ctx context.Context, scope Scope, in NewUnit) (Unit, error) {
	label := strings.TrimSpace(in.Label)
	csr := trim(in.CSR)
	if err := validate(label, in.Architecture, in.StoreID, csr); err != nil {
		return Unit{}, err
	}

	var out Unit
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if e := requireStore(ctx, tx, scope, in.StoreID); e != nil {
			return e
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO egs_unit
			  (tenant_id, company_id, store_id, label, architecture,
			   csr_common_name, csr_egs_serial_number, csr_organization_identifier,
			   csr_organization_unit, csr_organization_name, csr_country,
			   csr_invoice_type, csr_location, csr_industry)
			VALUES ($1,$2,$3,$4,$5,
			        nullif($6,''), nullif($7,''), nullif($8,''),
			        nullif($9,''), nullif($10,''), nullif($11,''),
			        nullif($12,''), nullif($13,''), nullif($14,''))
			RETURNING id`,
			scope.TenantID, scope.CompanyID, nullUUID(in.StoreID), label, in.Architecture,
			csr.CommonName, csr.EGSSerialNumber, csr.OrganizationIdentifier,
			csr.OrganizationUnit, csr.OrganizationName, csr.Country,
			csr.InvoiceType, csr.Location, csr.Industry).Scan(&id); e != nil {
			return db.Translate(e, "That e-invoicing unit could not be saved. "+
				"Another unit in this business may already use that name.")
		}

		u, e := s.read(ctx, tx, scope, id)
		out = u
		return e
	})
	return out, err
}

// Amend corrects a unit's name, branch and CSR details.
//
// The architecture is not amendable and neither is any CSID column. Everything
// else is, including after the chain has started: a shop that mistyped its VAT
// number must be able to fix it before onboarding, and a unit already signing
// under a wrong one is exactly the case that needs correcting most.
func (s *Service) Amend(
	ctx context.Context, scope Scope, id uuid.UUID, in Amendment,
) (Unit, error) {
	label := strings.TrimSpace(in.Label)
	csr := trim(in.CSR)

	var out Unit
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		current, e := s.read(ctx, tx, scope, id)
		if e != nil {
			return e
		}
		// Validated against the architecture it already has, because that is
		// what decides whether a branch is required, and against the branch it
		// will END UP in. An omitted store_id means "leave it where it is", so
		// a caller correcting only a CSR field must not be told to name a
		// branch the unit already has.
		store := in.StoreID
		if store == uuid.Nil {
			store, _ = uuid.Parse(current.StoreID)
		}
		if e := validate(label, current.Architecture, store, csr); e != nil {
			return e
		}
		if e := requireStore(ctx, tx, scope, in.StoreID); e != nil {
			return e
		}

		// Moving a branch unit is a correction, not a re-registration — the
		// same rule devices.Amend applies to a till — but the terminals signing
		// under it would then be in a different branch from their own unit, and
		// nothing else would notice. Refused while any till points here.
		if in.StoreID != uuid.Nil && current.StoreID != "" &&
			in.StoreID.String() != current.StoreID && current.Terminals > 0 {
			return errs.New(errs.CodeConflict,
				"Move the terminals signing under this unit to another one first. "+
					"A unit signs for the tills in its own branch.")
		}

		if _, e := tx.Exec(ctx, `
			UPDATE egs_unit SET
			  label = $3,
			  store_id = coalesce($4, store_id),
			  csr_common_name = nullif($5,''),
			  csr_egs_serial_number = nullif($6,''),
			  csr_organization_identifier = nullif($7,''),
			  csr_organization_unit = nullif($8,''),
			  csr_organization_name = nullif($9,''),
			  csr_country = nullif($10,''),
			  csr_invoice_type = nullif($11,''),
			  csr_location = nullif($12,''),
			  csr_industry = nullif($13,'')
			WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, label, nullUUID(in.StoreID),
			csr.CommonName, csr.EGSSerialNumber, csr.OrganizationIdentifier,
			csr.OrganizationUnit, csr.OrganizationName, csr.Country,
			csr.InvoiceType, csr.Location, csr.Industry); e != nil {
			return db.Translate(e, "That e-invoicing unit could not be saved. "+
				"Another unit in this business may already use that name.")
		}

		u, e := s.read(ctx, tx, scope, id)
		out = u
		return e
	})
	return out, err
}

// requireStore refuses a branch that is not this company's. Without it a caller
// could attach a signing unit to a branch of a business they cannot otherwise
// see — and the unit carries the VAT registration the chain hangs from.
func requireStore(ctx context.Context, tx pgx.Tx, scope Scope, storeID uuid.UUID) error {
	if storeID == uuid.Nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM store WHERE id = $1 AND company_id = $2
		)`, storeID, scope.CompanyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errs.New(errs.CodeNotFound, "That branch was not found in this business.")
	}
	return nil
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

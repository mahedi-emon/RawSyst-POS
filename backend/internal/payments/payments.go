// Package payments is the gateway-agnostic payment layer (blueprint E3.3,
// E3.4).
//
// # The adapter is code; the credentials are the client's
//
// E3.3 asks for an abstraction with adapters for the Saudi acquirers. None of
// that needs an account with any of them: an adapter is HTTP against a
// documented API, and the merchant id and key are configuration a shop types
// into a settings screen. A shop signs with Moyasar, pastes the key, presses
// Test, and the till takes cards through Moyasar — no deployment and no code
// change.
//
// # One interface, and it is deliberately small
//
// Charge, read back, refund. That is what a till needs and it is the largest
// surface every acquirer here genuinely shares. Tokenisation, subscriptions,
// payouts and dispute handling all differ enough between them that a common
// interface would be a lie with seven implementations.
//
// # Nothing here holds card numbers
//
// Every adapter either redirects the customer to the acquirer's own page or
// talks to a terminal on the counter. A PAN never reaches this process, which
// is what E3's "PCI-DSS scope minimisation" means in practice and is not a
// thing to be clever about.
//
// # A failed attempt is recorded
//
// `payment_attempt` holds what the ACQUIRER said, including the declines. A
// shop asking why a card failed three times is asking about that table, and a
// design that only recorded successes could not answer.
package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/secrets"
)

// Service configures gateways and drives them.
type Service struct {
	pool   *db.Pool
	cipher *secrets.Cipher

	// client is the HTTP client every adapter uses. One, with a timeout,
	// rather than http.DefaultClient — which has no timeout at all, and a till
	// waiting forever on an acquirer that stopped answering is a till nobody
	// can serve a customer from.
	client *http.Client
}

// NewService builds the service.
func NewService(pool *db.Pool, cipher *secrets.Cipher) *Service {
	return &Service{
		pool:   pool,
		cipher: cipher,
		// Twenty seconds. Long enough for an acquirer having a slow minute,
		// short enough that a cashier reaches for another tender rather than
		// standing there.
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Gateway is one configured acquirer, as a screen sees it.
//
// The secret is never in this type. There is no field for it and no route that
// returns one: a key that can be read back is a key that leaves in an export.
type Gateway struct {
	ID       uuid.UUID `json:"id"`
	Provider string    `json:"provider"`
	Label    string    `json:"label"`
	Mode     string    `json:"mode"`

	Settings map[string]string `json:"settings"`
	Methods  []string          `json:"methods"`
	IsActive bool              `json:"is_active"`

	// HasSecret says a key is stored without saying what it is, so a screen can
	// show "configured" and offer to replace it.
	HasSecret bool `json:"has_secret"`

	LastCheckedAt string `json:"last_checked_at,omitempty"`
	LastCheckOK   *bool  `json:"last_check_ok,omitempty"`
	LastCheckNote string `json:"last_check_note,omitempty"`
}

// Field is one thing a provider needs, for the screen to render a box for.
type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Secret marks the half that is sealed and never read back.
	Secret bool `json:"secret"`
	// Hint is where a shop finds this value in the acquirer's own dashboard,
	// which is the question somebody actually has in front of the form.
	Hint string `json:"hint,omitempty"`
}

// Provider describes one acquirer for the settings screen.
type Provider struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Fields  []Field  `json:"fields"`
	Methods []string `json:"methods"`
	// Docs is where the acquirer documents the values above.
	Docs string `json:"docs,omitempty"`
}

// Providers is what a shop can choose between.
//
// Data rather than a screen's hard-coded list, so adding an acquirer is an
// adapter and an entry here rather than a change in three places.
func Providers() []Provider {
	card := []string{"mada", "visa", "mastercard", "amex", "apple_pay"}
	return []Provider{
		{
			Key: "moyasar", Name: "Moyasar",
			Docs:    "https://docs.moyasar.com",
			Methods: append(append([]string{}, card...), "stc_pay"),
			Fields: []Field{
				{Key: "publishable_key", Label: "Publishable key",
					Hint: "Moyasar dashboard, under API keys"},
				{Key: "secret_key", Label: "Secret key", Secret: true,
					Hint: "Moyasar dashboard, under API keys"},
			},
		},
		{
			Key: "hyperpay", Name: "HyperPay",
			Docs:    "https://wordpresshyperpay.docs.oppwa.com",
			Methods: card,
			Fields: []Field{
				{Key: "entity_id", Label: "Entity ID",
					Hint: "One per card brand, from your HyperPay account"},
				{Key: "access_token", Label: "Access token", Secret: true},
			},
		},
		{
			Key: "paytabs", Name: "PayTabs",
			Docs:    "https://support.paytabs.com",
			Methods: append(append([]string{}, card...), "stc_pay"),
			Fields: []Field{
				{Key: "profile_id", Label: "Profile ID",
					Hint: "PayTabs merchant dashboard"},
				{Key: "region", Label: "Region",
					Hint: "SAU for Saudi Arabia"},
				{Key: "server_key", Label: "Server key", Secret: true},
			},
		},
		{
			Key: "tap", Name: "Tap Payments",
			Docs:    "https://developers.tap.company",
			Methods: append(append([]string{}, card...), "stc_pay"),
			Fields: []Field{
				{Key: "merchant_id", Label: "Merchant ID"},
				{Key: "secret_key", Label: "Secret key", Secret: true},
			},
		},
		{
			Key: "geidea", Name: "Geidea",
			Docs:    "https://docs.geidea.net",
			Methods: card,
			Fields: []Field{
				{Key: "merchant_public_key", Label: "Public key"},
				{Key: "api_password", Label: "API password", Secret: true},
			},
		},
		{
			Key: "checkout", Name: "Checkout.com",
			Docs:    "https://www.checkout.com/docs",
			Methods: card,
			Fields: []Field{
				{Key: "processing_channel_id", Label: "Processing channel ID"},
				{Key: "secret_key", Label: "Secret key", Secret: true},
			},
		},
		{
			Key: "amazon_payment_services", Name: "Amazon Payment Services",
			Docs:    "https://paymentservices.amazon.com/docs",
			Methods: card,
			Fields: []Field{
				{Key: "merchant_identifier", Label: "Merchant identifier"},
				{Key: "access_code", Label: "Access code"},
				{Key: "sha_request_phrase", Label: "SHA request phrase",
					Secret: true},
			},
		},
		{
			Key: "terminal", Name: "A card machine on the counter",
			Methods: card,
			Fields: []Field{
				{Key: "address", Label: "Address on the network",
					Hint: "For example 192.168.1.50:8080, from the machine"},
				{Key: "terminal_id", Label: "Terminal ID"},
			},
		},
	}
}

func providerByKey(key string) (Provider, bool) {
	for _, p := range Providers() {
		if p.Key == key {
			return p, true
		}
	}
	return Provider{}, false
}

// Gateways lists what a company has configured.
func (s *Service) Gateways(
	ctx context.Context, scope Scope,
) ([]Gateway, error) {
	out := []Gateway{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, provider::text, label, mode, settings::text, methods,
			       is_active, secret_enc IS NOT NULL, last_checked_at,
			       last_check_ok, coalesce(last_check_note, '')
			FROM payment_gateway
			WHERE company_id = $1
			ORDER BY is_active DESC, label`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var g Gateway
			var raw string
			var checked *time.Time
			if e := rows.Scan(&g.ID, &g.Provider, &g.Label, &g.Mode, &raw,
				&g.Methods, &g.IsActive, &g.HasSecret, &checked,
				&g.LastCheckOK, &g.LastCheckNote); e != nil {
				return e
			}
			g.Settings = map[string]string{}
			_ = json.Unmarshal([]byte(raw), &g.Settings)
			if checked != nil {
				g.LastCheckedAt = checked.UTC().Format(time.RFC3339)
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// NewGateway is a configuration being saved.
type NewGateway struct {
	ID       uuid.UUID
	Provider string
	Label    string
	Mode     string
	Settings map[string]string
	// Secret is the sealed half. Empty on an edit means "leave what is there",
	// because a screen that cannot read a key back cannot send it again.
	Secret   string
	Methods  []string
	IsActive bool
}

// SaveGateway creates or edits one.
func (s *Service) SaveGateway(
	ctx context.Context, scope Scope, in NewGateway,
) (Gateway, error) {
	provider, ok := providerByKey(in.Provider)
	if !ok {
		return Gateway{}, errs.New(errs.CodeInvalidInput,
			"That is not a card provider this product can talk to.")
	}
	if strings.TrimSpace(in.Label) == "" {
		return Gateway{}, errs.New(errs.CodeInvalidInput,
			"Give the connection a name, so two accounts with the same "+
				"provider can be told apart.")
	}
	if in.Mode != "test" && in.Mode != "live" {
		in.Mode = "test"
	}

	// Every public field the provider names has to be present. A configuration
	// missing one fails at the counter rather than here, which is the wrong
	// place to find out.
	if in.Settings == nil {
		in.Settings = map[string]string{}
	}
	missing := []string{}
	for _, f := range provider.Fields {
		if f.Secret {
			continue
		}
		if strings.TrimSpace(in.Settings[f.Key]) == "" {
			missing = append(missing, f.Label)
		}
	}
	if len(missing) > 0 {
		return Gateway{}, errs.Newf(errs.CodeInvalidInput,
			"%s still needs: %s.", provider.Name, strings.Join(missing, ", "))
	}

	var sealed []byte
	if strings.TrimSpace(in.Secret) != "" {
		if s.cipher == nil {
			return Gateway{}, errs.New(errs.CodeUnavailable,
				"This installation has no encryption key, so a payment key "+
					"cannot be stored.")
		}
		var err error
		sealed, err = s.cipher.Seal([]byte(strings.TrimSpace(in.Secret)))
		if err != nil {
			return Gateway{}, err
		}
	}

	settings, err := json.Marshal(in.Settings)
	if err != nil {
		return Gateway{}, err
	}

	var id uuid.UUID
	err = s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		if in.ID != uuid.Nil {
			id = in.ID
			// The secret is only overwritten when a new one was typed. A blank
			// box on an edit means "leave it", because the screen could not
			// have shown it in the first place.
			tag, e := tx.Exec(ctx, `
				UPDATE payment_gateway
				   SET provider = $3::payment_provider, label = $4, mode = $5,
				       settings = $6::jsonb, methods = $7, is_active = $8,
				       secret_enc = coalesce($9, secret_enc)
				 WHERE id = $1 AND company_id = $2`,
				id, scope.CompanyID, in.Provider, strings.TrimSpace(in.Label),
				in.Mode, string(settings), in.Methods, in.IsActive, sealed)
			if e != nil {
				return db.Translate(e, "That connection could not be saved.")
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That connection was not found.")
			}
		} else {
			if e := tx.QueryRow(ctx, `
				INSERT INTO payment_gateway (
				  tenant_id, company_id, provider, label, mode, settings,
				  methods, is_active, secret_enc, created_by)
				VALUES ($1,$2,$3::payment_provider,$4,$5,$6::jsonb,$7,$8,$9,$10)
				RETURNING id`,
				scope.TenantID, scope.CompanyID, in.Provider,
				strings.TrimSpace(in.Label), in.Mode, string(settings),
				in.Methods, in.IsActive, sealed, scope.UserID).
				Scan(&id); e != nil {
				return db.Translate(e, "That connection could not be saved.")
			}
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "payment_gateway_saved",
			EntityType: "payment_gateway", EntityID: &id,
			// The settings are recorded and the secret is not, which is the
			// same line every other credential in this product draws.
			After: map[string]any{
				"provider": in.Provider, "mode": in.Mode,
				"label": in.Label, "active": in.IsActive,
			},
		})
	})
	if err != nil {
		return Gateway{}, db.Translate(err, "")
	}
	return s.Gateway(ctx, scope, id)
}

// Gateway reads one.
func (s *Service) Gateway(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Gateway, error) {
	list, err := s.Gateways(ctx, scope)
	if err != nil {
		return Gateway{}, err
	}
	for _, g := range list {
		if g.ID == id {
			return g, nil
		}
	}
	return Gateway{}, errs.New(errs.CodeNotFound,
		"That connection was not found.")
}

// RemoveGateway deletes a configuration.
//
// Refused once anything has been charged through it: the attempts reference it,
// and a deleted acquirer would leave a shop unable to answer what took a
// payment six months ago.
func (s *Service) RemoveGateway(
	ctx context.Context, scope Scope, id uuid.UUID,
) error {
	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			var used int
			if e := tx.QueryRow(ctx,
				`SELECT count(*) FROM payment_attempt WHERE gateway_id = $1`,
				id).Scan(&used); e != nil {
				return e
			}
			if used > 0 {
				return errs.Newf(errs.CodeConflict,
					"%d payments went through that connection, so it cannot "+
						"be removed. Switch it off instead.", used)
			}
			tag, e := tx.Exec(ctx,
				`DELETE FROM payment_gateway
				  WHERE id = $1 AND company_id = $2`, id, scope.CompanyID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That connection was not found.")
			}
			return nil
		}), "")
}

// secretFor opens the sealed half.
func (s *Service) secretFor(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (config, error) {
	var c config
	var raw string
	var sealed []byte
	e := tx.QueryRow(ctx, `
		SELECT provider::text, mode, settings::text, secret_enc
		FROM payment_gateway
		WHERE id = $1 AND company_id = $2`,
		id, companyID).Scan(&c.provider, &c.mode, &raw, &sealed)
	if errors.Is(e, pgx.ErrNoRows) {
		return config{}, errs.New(errs.CodeNotFound,
			"That connection was not found.")
	}
	if e != nil {
		return config{}, e
	}

	c.settings = map[string]string{}
	_ = json.Unmarshal([]byte(raw), &c.settings)

	if len(sealed) > 0 {
		if s.cipher == nil {
			return config{}, errs.New(errs.CodeUnavailable,
				"This installation has no encryption key, so the payment key "+
					"cannot be read.")
		}
		plain, err := s.cipher.Open(sealed)
		if err != nil {
			return config{}, err
		}
		c.secret = string(plain)
	}
	return c, nil
}

// config is one gateway's credentials, decrypted, for an adapter to use.
type config struct {
	provider string
	mode     string
	settings map[string]string
	secret   string
}

func (c config) live() bool { return c.mode == "live" }

// Check asks the acquirer whether the credentials work.
//
// The result is stored on the row, because a connection that stopped working
// should be visible on the settings screen rather than discovered by a cashier
// at the counter. A live configuration cannot be switched on until this has
// passed, which the table's own CHECK enforces.
func (s *Service) Check(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Gateway, error) {
	var cfg config
	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var e error
		cfg, e = s.secretFor(ctx, tx, scope.CompanyID, id)
		return e
	}); err != nil {
		return Gateway{}, db.Translate(err, "")
	}

	adapter, err := adapterFor(cfg.provider)
	if err != nil {
		return Gateway{}, err
	}

	note := ""
	ok := true
	if e := adapter.Check(ctx, s.client, cfg); e != nil {
		ok = false
		note = e.Error()
	}

	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE payment_gateway
			   SET last_checked_at = now(), last_check_ok = $3,
			       last_check_note = nullif($4,''),
			       -- A configuration that stops answering is switched off
			       -- rather than left on: a till that thinks it can take cards
			       -- and cannot is worse than one that knows it cannot.
			       is_active = CASE WHEN $3 THEN is_active ELSE false END
			 WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, ok, note)
		return e
	}); err != nil {
		return Gateway{}, db.Translate(err, "")
	}

	return s.Gateway(ctx, scope, id)
}

// Attempt is one try at taking money.
type Attempt struct {
	ID       uuid.UUID `json:"id"`
	Method   string    `json:"method"`
	Amount   string    `json:"amount"`
	Currency string    `json:"currency"`
	Status   string    `json:"status"`

	ProviderRef     string `json:"provider_ref,omitempty"`
	ProviderCode    string `json:"provider_code,omitempty"`
	ProviderMessage string `json:"provider_message,omitempty"`

	// RedirectURL is where the customer goes for a hosted checkout. Present
	// only while the attempt is open.
	RedirectURL string `json:"redirect_url,omitempty"`

	CreatedAt string `json:"created_at"`
	SettledAt string `json:"settled_at,omitempty"`
}

// Charge takes money through a configured gateway.
//
// `idempotency` is the caller's own uuid. A till that retries after a timeout
// sends the same one and gets the same attempt back rather than charging the
// customer twice — which is the single most important property here.
func (s *Service) Charge(
	ctx context.Context, scope Scope, gatewayID, idempotency uuid.UUID,
	method string, amount decimal.Decimal, currency string,
	invoiceID *uuid.UUID, returnURL string,
) (Attempt, error) {
	if !amount.IsPositive() {
		return Attempt{}, errs.New(errs.CodeInvalidInput,
			"A charge of nothing is not a charge.")
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return Attempt{}, errs.New(errs.CodeInvalidInput,
			"Say which currency the charge is in.")
	}

	// Already done? The retry path, checked before anything is sent.
	if existing, found, err := s.attemptByUUID(
		ctx, scope, idempotency); err != nil {
		return Attempt{}, err
	} else if found {
		return existing, nil
	}

	var cfg config
	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var active bool
		if e := tx.QueryRow(ctx,
			`SELECT is_active FROM payment_gateway
			  WHERE id = $1 AND company_id = $2`,
			gatewayID, scope.CompanyID).Scan(&active); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"That connection was not found.")
			}
			return e
		}
		if !active {
			return errs.New(errs.CodeConflict,
				"That card provider is switched off.")
		}
		var e error
		cfg, e = s.secretFor(ctx, tx, scope.CompanyID, gatewayID)
		return e
	}); err != nil {
		return Attempt{}, db.Translate(err, "")
	}

	adapter, err := adapterFor(cfg.provider)
	if err != nil {
		return Attempt{}, err
	}

	// The attempt row FIRST, so a charge that is sent and whose answer is lost
	// leaves a trace. The alternative — write on success — loses exactly the
	// case that matters, which is the one where the customer was charged and
	// the shop does not know.
	var id uuid.UUID
	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO payment_attempt (
			  tenant_id, company_id, gateway_id, uuid, invoice_id, method,
			  amount, currency)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, gatewayID, idempotency,
			invoiceID, method, amount, currency).Scan(&id)
	}); err != nil {
		return Attempt{}, db.Translate(err, "That charge could not be started.")
	}

	result := adapter.Charge(ctx, s.client, cfg, ChargeRequest{
		Reference: id.String(),
		Amount:    amount,
		Currency:  currency,
		Method:    method,
		ReturnURL: returnURL,
	})

	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE payment_attempt
			   SET status = $2, provider_ref = nullif($3,''),
			       provider_code = nullif($4,''),
			       provider_message = nullif($5,''),
			       redirect_url = nullif($6,''),
			       settled_at = CASE WHEN $2 IN ('captured', 'failed',
			                                     'cancelled')
			                         THEN now() ELSE NULL END
			 WHERE id = $1`,
			id, result.Status, result.Reference, result.Code,
			result.Message, result.RedirectURL)
		return e
	}); err != nil {
		return Attempt{}, db.Translate(err, "")
	}

	return s.attempt(ctx, scope, id)
}

func (s *Service) attemptByUUID(
	ctx context.Context, scope Scope, key uuid.UUID,
) (Attempt, bool, error) {
	var out Attempt
	found := false
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, attemptSelect+`
			WHERE company_id = $1 AND uuid = $2`, scope.CompanyID, key)
		a, e := scanAttempt(row)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		out = a
		found = true
		return nil
	})
	return out, found, db.Translate(err, "")
}

func (s *Service) attempt(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Attempt, error) {
	var out Attempt
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, attemptSelect+`
			WHERE company_id = $1 AND id = $2`, scope.CompanyID, id)
		a, e := scanAttempt(row)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That charge was not found.")
		}
		out = a
		return e
	})
	return out, db.Translate(err, "")
}

// Attempts lists what has been tried, most recent first.
func (s *Service) Attempts(
	ctx context.Context, scope Scope,
) ([]Attempt, error) {
	out := []Attempt{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, attemptSelect+`
			WHERE company_id = $1
			ORDER BY created_at DESC
			LIMIT 200`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			a, e := scanAttempt(rows)
			if e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

const attemptSelect = `
	SELECT id, method, amount, currency, status,
	       coalesce(provider_ref, ''), coalesce(provider_code, ''),
	       coalesce(provider_message, ''), coalesce(redirect_url, ''),
	       created_at, settled_at
	FROM payment_attempt`

type scanner interface {
	Scan(dst ...any) error
}

func scanAttempt(row scanner) (Attempt, error) {
	var a Attempt
	var amount decimal.Decimal
	var created time.Time
	var settled *time.Time
	if err := row.Scan(&a.ID, &a.Method, &amount, &a.Currency, &a.Status,
		&a.ProviderRef, &a.ProviderCode, &a.ProviderMessage, &a.RedirectURL,
		&created, &settled); err != nil {
		return Attempt{}, err
	}
	a.Amount = amount.StringFixed(2)
	a.CreatedAt = created.UTC().Format(time.RFC3339)
	if settled != nil {
		a.SettledAt = settled.UTC().Format(time.RFC3339)
	}
	return a, nil
}

// Refund sends money back through the gateway that took it.
func (s *Service) Refund(
	ctx context.Context, scope Scope, attemptID uuid.UUID,
	amount decimal.Decimal,
) (Attempt, error) {
	var cfg config
	var ref, currency string
	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var gatewayID uuid.UUID
		var status string
		e := tx.QueryRow(ctx, `
			SELECT gateway_id, coalesce(provider_ref, ''), currency, status
			FROM payment_attempt
			WHERE id = $1 AND company_id = $2`,
			attemptID, scope.CompanyID).Scan(
			&gatewayID, &ref, &currency, &status)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That charge was not found.")
		}
		if e != nil {
			return e
		}
		if status != "captured" {
			return errs.Newf(errs.CodeConflict,
				"That charge is %s, so there is nothing to send back.", status)
		}
		if ref == "" {
			return errs.New(errs.CodeConflict,
				"That charge has no reference from the provider, so it "+
					"cannot be refunded through them.")
		}
		cfg, e = s.secretFor(ctx, tx, scope.CompanyID, gatewayID)
		return e
	}); err != nil {
		return Attempt{}, db.Translate(err, "")
	}

	adapter, err := adapterFor(cfg.provider)
	if err != nil {
		return Attempt{}, err
	}

	result := adapter.Refund(ctx, s.client, cfg, RefundRequest{
		Reference: ref, Amount: amount, Currency: currency,
	})

	if err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE payment_attempt
			   SET status = $2, provider_code = nullif($3,''),
			       provider_message = nullif($4,''), settled_at = now()
			 WHERE id = $1 AND company_id = $5`,
			attemptID, result.Status, result.Code, result.Message,
			scope.CompanyID)
		return e
	}); err != nil {
		return Attempt{}, db.Translate(err, "")
	}

	return s.attempt(ctx, scope, attemptID)
}

// unsupported is what an adapter returns for something it cannot do.
func unsupported(provider, what string) error {
	return errs.Newf(errs.CodeUnavailable,
		"%s cannot %s through this product yet.", provider, what)
}

package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The adapters, one per acquirer (blueprint E3.3).
//
// # Each is the same three calls against a different shape of JSON
//
// Check, Charge, Refund. The differences between acquirers are entirely in the
// endpoint, the authentication header and the field names — which is exactly
// what an abstraction layer is for, and exactly why the layer is thin.
//
// # Amounts, and the halala problem
//
// Most of these want the smallest unit as an integer: 115.00 SAR is 11500. One
// of them wants a decimal string. Getting this wrong charges a customer a
// hundred times too much or a hundredth, so the conversion is one function with
// the currency's exponent in it rather than a `* 100` at seven call sites.
//
// The three-decimal currencies matter here: Bahraini, Kuwaiti and Omani money
// has 1000 minor units, and a Saudi shop selling across the Gulf will meet
// them.
//
// # An error from the acquirer is not an error from this product
//
// A declined card is a normal Tuesday. It comes back as a `Result` with the
// acquirer's own code and message, not as a Go error — errors here are reserved
// for the network being down and the configuration being wrong, which are the
// two things a shop can actually do something about.

// ChargeRequest is what every adapter is asked to take.
type ChargeRequest struct {
	// Reference is this product's own attempt id, sent so a reconciliation can
	// match the acquirer's record back to ours.
	Reference string
	Amount    decimal.Decimal
	Currency  string
	Method    string
	// ReturnURL is where the customer comes back to after a hosted page.
	ReturnURL string
}

// RefundRequest sends money back.
type RefundRequest struct {
	// Reference is the ACQUIRER's id for the original charge.
	Reference string
	Amount    decimal.Decimal
	Currency  string
}

// Result is what came back.
type Result struct {
	// Status is one of the payment_attempt states.
	Status string
	// Reference is the acquirer's own id, which a support call quotes.
	Reference string
	Code      string
	Message   string
	// RedirectURL is where to send the customer, for a hosted checkout.
	RedirectURL string
}

// Adapter is one acquirer.
type Adapter interface {
	// Check proves the credentials work, without moving money.
	Check(ctx context.Context, c *http.Client, cfg config) error
	Charge(ctx context.Context, c *http.Client, cfg config,
		req ChargeRequest) Result
	Refund(ctx context.Context, c *http.Client, cfg config,
		req RefundRequest) Result
}

func adapterFor(provider string) (Adapter, error) {
	switch provider {
	case "moyasar":
		return moyasar{}, nil
	case "hyperpay":
		return hyperpay{}, nil
	case "paytabs":
		return paytabs{}, nil
	case "tap":
		return tap{}, nil
	case "geidea":
		return geidea{}, nil
	case "checkout":
		return checkout{}, nil
	case "amazon_payment_services":
		return amazonPS{}, nil
	case "terminal":
		return terminal{}, nil
	}
	return nil, errs.Newf(errs.CodeInvalidInput,
		"There is no adapter for %q.", provider)
}

// exponent is how many minor units a currency has.
//
// Two for most, three for the Gulf currencies a Saudi shop meets across the
// causeway, zero for the ones with no subdivision in practice. Getting this
// wrong charges a customer a hundred times too much, so it is a table rather
// than a constant.
func exponent(currency string) int32 {
	switch strings.ToUpper(currency) {
	case "BHD", "KWD", "OMR", "JOD", "TND", "IQD", "LYD":
		return 3
	case "JPY", "KRW", "VND", "CLP", "ISK":
		return 0
	}
	return 2
}

// minorUnits turns an amount into the integer an acquirer expects.
func minorUnits(amount decimal.Decimal, currency string) int64 {
	return amount.Shift(exponent(currency)).Round(0).IntPart()
}

// decimalString is the other convention: the amount as it is written.
func decimalString(amount decimal.Decimal, currency string) string {
	return amount.StringFixed(exponent(currency))
}

// call sends a JSON request and returns the decoded body and the status.
//
// One place, so the timeout, the body limit and the error shape are one
// decision rather than eight.
func call(
	ctx context.Context, c *http.Client, method, endpoint string,
	headers map[string]string, body any,
) (map[string]any, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, errs.Wrap(err, errs.CodeUnavailable,
			"The card provider could not be reached.")
	}
	defer resp.Body.Close()

	// Capped. An acquirer having a bad day and streaming an HTML error page
	// should not become memory pressure on the till's server.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}

	out := map[string]any{}
	// A body that is not JSON is not a failure on its own: some acquirers
	// answer a health check with an empty 200.
	_ = json.Unmarshal(raw, &out)
	return out, resp.StatusCode, nil
}

// str reads a string out of a decoded body, following a dotted path.
func str(body map[string]any, path string) string {
	var cur any = body
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

// failed is the Result for something that did not work.
func failed(code, message string) Result {
	return Result{Status: "failed", Code: code, Message: message}
}

// unreachable is the Result for a network failure, which is not a decline.
func unreachable(err error) Result {
	return Result{
		Status: "failed", Code: "unreachable", Message: err.Error(),
	}
}

// --- Moyasar --------------------------------------------------------------
//
// HTTP basic with the secret key as the username. Amounts in halalas.

type moyasar struct{}

func (moyasar) base() string { return "https://api.moyasar.com/v1" }

func (m moyasar) auth(cfg config) map[string]string {
	return map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString(
			[]byte(cfg.secret+":")),
	}
}

func (m moyasar) Check(
	ctx context.Context, c *http.Client, cfg config,
) error {
	// Listing one payment proves the key is accepted without creating
	// anything, which is what a Test button should do.
	_, status, err := call(ctx, c, http.MethodGet,
		m.base()+"/payments?limit=1", m.auth(cfg), nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return errs.New(errs.CodeUnauthenticated,
			"Moyasar refused that secret key.")
	}
	if status >= 400 {
		return errs.Newf(errs.CodeUnavailable,
			"Moyasar answered %d.", status)
	}
	return nil
}

func (m moyasar) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost, m.base()+"/payments",
		m.auth(cfg), map[string]any{
			"amount":       minorUnits(req.Amount, req.Currency),
			"currency":     req.Currency,
			"description":  req.Reference,
			"callback_url": req.ReturnURL,
			"source":       map[string]any{"type": "creditcard"},
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "type"), str(body, "message"))
	}
	return Result{
		Status:      moyasarStatus(str(body, "status")),
		Reference:   str(body, "id"),
		Code:        str(body, "status"),
		Message:     str(body, "source.message"),
		RedirectURL: str(body, "source.transaction_url"),
	}
}

func moyasarStatus(s string) string {
	switch s {
	case "paid":
		return "captured"
	case "authorized":
		return "authorised"
	case "failed":
		return "failed"
	case "refunded":
		return "refunded"
	}
	return "initiated"
}

func (m moyasar) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		m.base()+"/payments/"+url.PathEscape(req.Reference)+"/refund",
		m.auth(cfg), map[string]any{
			"amount": minorUnits(req.Amount, req.Currency),
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "type"), str(body, "message"))
	}
	return Result{Status: "refunded", Reference: str(body, "id")}
}

// --- HyperPay -------------------------------------------------------------
//
// Bearer token, form-encoded rather than JSON, and one entity id per card
// brand. Amounts as a decimal string.

type hyperpay struct{}

func (h hyperpay) base(cfg config) string {
	if cfg.live() {
		return "https://oppwa.com/v1"
	}
	return "https://eu-test.oppwa.com/v1"
}

func (h hyperpay) form(
	ctx context.Context, c *http.Client, cfg config, method, endpoint string,
	values url.Values,
) (map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint,
		strings.NewReader(values.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, errs.Wrap(err, errs.CodeUnavailable,
			"HyperPay could not be reached.")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out, resp.StatusCode, nil
}

func (h hyperpay) Check(
	ctx context.Context, c *http.Client, cfg config,
) error {
	// A checkout with no amount is refused for a reason that proves the token
	// and the entity were accepted, without creating a payment. HyperPay
	// answers 200 with a result code either way, so the code is what is read.
	body, status, err := h.form(ctx, c, cfg, http.MethodPost,
		h.base(cfg)+"/checkouts", url.Values{
			"entityId":    {cfg.settings["entity_id"]},
			"amount":      {"1.00"},
			"currency":    {"SAR"},
			"paymentType": {"DB"},
		})
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return errs.New(errs.CodeUnauthenticated,
			"HyperPay refused that access token.")
	}
	code := str(body, "result.code")
	// 000.200.100 is "successfully created checkout". Anything in the 800
	// family is a configuration or authentication problem worth reporting as
	// it was said.
	if strings.HasPrefix(code, "000.") || strings.HasPrefix(code, "200.") {
		return nil
	}
	return errs.Newf(errs.CodeUnavailable, "HyperPay said: %s",
		str(body, "result.description"))
}

func (h hyperpay) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := h.form(ctx, c, cfg, http.MethodPost,
		h.base(cfg)+"/checkouts", url.Values{
			"entityId":              {cfg.settings["entity_id"]},
			"amount":                {decimalString(req.Amount, req.Currency)},
			"currency":              {req.Currency},
			"paymentType":           {"DB"},
			"merchantTransactionId": {req.Reference},
			"shopperResultUrl":      {req.ReturnURL},
		})
	if err != nil {
		return unreachable(err)
	}
	code := str(body, "result.code")
	if status >= 400 || !(strings.HasPrefix(code, "000.") ||
		strings.HasPrefix(code, "200.")) {
		return failed(code, str(body, "result.description"))
	}
	id := str(body, "id")
	return Result{
		// A checkout id is not a captured payment: the customer has not paid
		// yet. Reporting it as captured is how a shop hands over goods for
		// money nobody took.
		Status:    "initiated",
		Reference: id,
		Code:      code,
		Message:   str(body, "result.description"),
		RedirectURL: h.base(cfg) + "/paymentWidgets.js?checkoutId=" +
			url.QueryEscape(id),
	}
}

func (h hyperpay) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := h.form(ctx, c, cfg, http.MethodPost,
		h.base(cfg)+"/payments/"+url.PathEscape(req.Reference), url.Values{
			"entityId":    {cfg.settings["entity_id"]},
			"amount":      {decimalString(req.Amount, req.Currency)},
			"currency":    {req.Currency},
			"paymentType": {"RF"},
		})
	if err != nil {
		return unreachable(err)
	}
	code := str(body, "result.code")
	if status >= 400 || !strings.HasPrefix(code, "000.") {
		return failed(code, str(body, "result.description"))
	}
	return Result{Status: "refunded", Reference: str(body, "id"), Code: code}
}

// --- PayTabs --------------------------------------------------------------
//
// The server key in an Authorization header, and a region-specific host.

type paytabs struct{}

func (p paytabs) base(cfg config) string {
	// PayTabs runs a host per region and the Saudi one is `secure`. A region a
	// shop typed that this does not know falls back to it rather than guessing
	// a hostname that does not resolve.
	switch strings.ToUpper(strings.TrimSpace(cfg.settings["region"])) {
	case "ARE":
		return "https://secure.paytabs.com"
	case "EGY":
		return "https://secure-egypt.paytabs.com"
	case "OMN":
		return "https://secure-oman.paytabs.com"
	case "JOR":
		return "https://secure-jordan.paytabs.com"
	case "GLOBAL":
		return "https://secure-global.paytabs.com"
	}
	return "https://secure.paytabs.com"
}

func (p paytabs) auth(cfg config) map[string]string {
	return map[string]string{"Authorization": cfg.secret}
}

func (p paytabs) Check(
	ctx context.Context, c *http.Client, cfg config,
) error {
	// A query for a transaction that does not exist. The point is the
	// authentication, and PayTabs answers a bad key with 401 before it looks
	// at the reference.
	_, status, err := call(ctx, c, http.MethodPost,
		p.base(cfg)+"/payment/query", p.auth(cfg), map[string]any{
			"profile_id": cfg.settings["profile_id"],
			"tran_ref":   "rawsyst-connection-check",
		})
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return errs.New(errs.CodeUnauthenticated,
			"PayTabs refused that server key.")
	}
	if status >= 500 {
		return errs.Newf(errs.CodeUnavailable, "PayTabs answered %d.", status)
	}
	return nil
}

func (p paytabs) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		p.base(cfg)+"/payment/request", p.auth(cfg), map[string]any{
			"profile_id":       cfg.settings["profile_id"],
			"tran_type":        "sale",
			"tran_class":       "ecom",
			"cart_id":          req.Reference,
			"cart_description": req.Reference,
			"cart_currency":    req.Currency,
			"cart_amount":      decimalString(req.Amount, req.Currency),
			"return":           req.ReturnURL,
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "code"), str(body, "message"))
	}
	return Result{
		Status:      "initiated",
		Reference:   str(body, "tran_ref"),
		RedirectURL: str(body, "redirect_url"),
		Message:     str(body, "payment_result.response_message"),
	}
}

func (p paytabs) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		p.base(cfg)+"/payment/request", p.auth(cfg), map[string]any{
			"profile_id":       cfg.settings["profile_id"],
			"tran_type":        "refund",
			"tran_class":       "ecom",
			"tran_ref":         req.Reference,
			"cart_id":          req.Reference,
			"cart_currency":    req.Currency,
			"cart_amount":      decimalString(req.Amount, req.Currency),
			"cart_description": "refund",
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "code"), str(body, "message"))
	}
	return Result{Status: "refunded", Reference: str(body, "tran_ref")}
}

// --- Tap ------------------------------------------------------------------

type tap struct{}

func (tap) base() string { return "https://api.tap.company/v2" }

func (t tap) auth(cfg config) map[string]string {
	return map[string]string{"Authorization": "Bearer " + cfg.secret}
}

func (t tap) Check(ctx context.Context, c *http.Client, cfg config) error {
	_, status, err := call(ctx, c, http.MethodGet,
		t.base()+"/merchants/"+url.PathEscape(cfg.settings["merchant_id"]),
		t.auth(cfg), nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return errs.New(errs.CodeUnauthenticated,
			"Tap refused that secret key.")
	}
	if status >= 500 {
		return errs.Newf(errs.CodeUnavailable, "Tap answered %d.", status)
	}
	return nil
}

func (t tap) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost, t.base()+"/charges",
		t.auth(cfg), map[string]any{
			// Tap takes the amount as a decimal number rather than minor
			// units, which is the one exception among these seven.
			"amount":      req.Amount.InexactFloat64(),
			"currency":    req.Currency,
			"reference":   map[string]any{"transaction": req.Reference},
			"source":      map[string]any{"id": "src_all"},
			"redirect":    map[string]any{"url": req.ReturnURL},
			"merchant":    map[string]any{"id": cfg.settings["merchant_id"]},
			"description": req.Reference,
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "errors.0.code"), str(body, "errors.0.description"))
	}
	return Result{
		Status:      tapStatus(str(body, "status")),
		Reference:   str(body, "id"),
		Code:        str(body, "status"),
		Message:     str(body, "response.message"),
		RedirectURL: str(body, "transaction.url"),
	}
}

func tapStatus(s string) string {
	switch strings.ToUpper(s) {
	case "CAPTURED":
		return "captured"
	case "AUTHORIZED":
		return "authorised"
	case "DECLINED", "FAILED", "ABANDONED", "RESTRICTED":
		return "failed"
	case "CANCELLED", "VOID":
		return "cancelled"
	}
	return "initiated"
}

func (t tap) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost, t.base()+"/refunds",
		t.auth(cfg), map[string]any{
			"charge_id": req.Reference,
			"amount":    req.Amount.InexactFloat64(),
			"currency":  req.Currency,
			"reason":    "requested_by_customer",
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "errors.0.code"),
			str(body, "errors.0.description"))
	}
	return Result{Status: "refunded", Reference: str(body, "id")}
}

// --- Geidea ---------------------------------------------------------------

type geidea struct{}

func (geidea) base() string { return "https://api.merchant.geidea.net" }

func (g geidea) auth(cfg config) map[string]string {
	// Basic, with the public key as the username and the API password as the
	// password, which is what Geidea's own documentation shows.
	pair := cfg.settings["merchant_public_key"] + ":" + cfg.secret
	return map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString(
			[]byte(pair)),
	}
}

func (g geidea) Check(ctx context.Context, c *http.Client, cfg config) error {
	_, status, err := call(ctx, c, http.MethodGet,
		g.base()+"/payment-intent/api/v1/direct/config", g.auth(cfg), nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return errs.New(errs.CodeUnauthenticated,
			"Geidea refused those credentials.")
	}
	if status >= 500 {
		return errs.Newf(errs.CodeUnavailable, "Geidea answered %d.", status)
	}
	return nil
}

func (g geidea) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		g.base()+"/payment-intent/api/v2/direct/session", g.auth(cfg),
		map[string]any{
			"amount":              req.Amount.InexactFloat64(),
			"currency":            req.Currency,
			"merchantReferenceId": req.Reference,
			"callbackUrl":         req.ReturnURL,
			"returnUrl":           req.ReturnURL,
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "responseCode"),
			str(body, "detailedResponseMessage"))
	}
	return Result{
		Status:      "initiated",
		Reference:   str(body, "session.id"),
		Code:        str(body, "responseCode"),
		Message:     str(body, "responseMessage"),
		RedirectURL: str(body, "session.redirectUrl"),
	}
}

func (g geidea) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		g.base()+"/pgw/api/v1/direct/refund", g.auth(cfg), map[string]any{
			"orderId":  req.Reference,
			"amount":   req.Amount.InexactFloat64(),
			"currency": req.Currency,
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "responseCode"),
			str(body, "detailedResponseMessage"))
	}
	return Result{Status: "refunded", Reference: str(body, "order.id")}
}

// --- Checkout.com ---------------------------------------------------------

type checkout struct{}

func (c checkout) base(cfg config) string {
	if cfg.live() {
		return "https://api.checkout.com"
	}
	return "https://api.sandbox.checkout.com"
}

func (c checkout) auth(cfg config) map[string]string {
	return map[string]string{"Authorization": "Bearer " + cfg.secret}
}

func (co checkout) Check(
	ctx context.Context, c *http.Client, cfg config,
) error {
	_, status, err := call(ctx, c, http.MethodGet,
		co.base(cfg)+"/workflows", co.auth(cfg), nil)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return errs.New(errs.CodeUnauthenticated,
			"Checkout.com refused that secret key.")
	}
	if status >= 500 {
		return errs.Newf(errs.CodeUnavailable,
			"Checkout.com answered %d.", status)
	}
	return nil
}

func (co checkout) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		co.base(cfg)+"/hosted-payments", co.auth(cfg), map[string]any{
			"amount":                minorUnits(req.Amount, req.Currency),
			"currency":              req.Currency,
			"reference":             req.Reference,
			"success_url":           req.ReturnURL,
			"failure_url":           req.ReturnURL,
			"cancel_url":            req.ReturnURL,
			"processing_channel_id": cfg.settings["processing_channel_id"],
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "error_type"),
			strings.TrimSpace(str(body, "error_codes.0")))
	}
	return Result{
		Status:      "initiated",
		Reference:   str(body, "id"),
		RedirectURL: str(body, "_links.redirect.href"),
	}
}

func (co checkout) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		co.base(cfg)+"/payments/"+url.PathEscape(req.Reference)+"/refunds",
		co.auth(cfg), map[string]any{
			"amount": minorUnits(req.Amount, req.Currency),
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "error_type"), str(body, "error_codes.0"))
	}
	return Result{Status: "refunded", Reference: str(body, "action_id")}
}

// --- Amazon Payment Services ----------------------------------------------

type amazonPS struct{}

func (amazonPS) base(cfg config) string {
	if cfg.live() {
		return "https://paymentservices.payfort.com/FortAPI/paymentApi"
	}
	return "https://sbpaymentservices.payfort.com/FortAPI/paymentApi"
}

func (a amazonPS) Check(
	ctx context.Context, c *http.Client, cfg config,
) error {
	// APS has no read-only endpoint that proves a credential without a
	// signature over a full request, and signing a fake one would be sending
	// a payment nobody asked for. So the check is what can honestly be
	// checked: that the fields are present and the host answers.
	for _, key := range []string{"merchant_identifier", "access_code"} {
		if strings.TrimSpace(cfg.settings[key]) == "" {
			return errs.Newf(errs.CodeInvalidInput,
				"Amazon Payment Services still needs the %s.",
				strings.ReplaceAll(key, "_", " "))
		}
	}
	if strings.TrimSpace(cfg.secret) == "" {
		return errs.New(errs.CodeInvalidInput,
			"Amazon Payment Services still needs the SHA request phrase.")
	}
	_, status, err := call(ctx, c, http.MethodGet, a.base(cfg), nil, nil)
	if err != nil {
		return err
	}
	if status >= 500 {
		return errs.Newf(errs.CodeUnavailable,
			"Amazon Payment Services answered %d.", status)
	}
	// Said plainly rather than reported as a pass: the credentials are present
	// and the host is up, and the first real charge is what proves the rest.
	return errs.New(errs.CodeUnavailable,
		"The details are complete and the host answered. Amazon Payment "+
			"Services offers no way to prove a key without sending a "+
			"payment, so the first live charge is the real test.")
}

func (a amazonPS) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost, a.base(cfg), nil,
		map[string]any{
			"command":             "PURCHASE",
			"access_code":         cfg.settings["access_code"],
			"merchant_identifier": cfg.settings["merchant_identifier"],
			"merchant_reference":  req.Reference,
			"amount":              minorUnits(req.Amount, req.Currency),
			"currency":            req.Currency,
			"language":            "en",
			"return_url":          req.ReturnURL,
			"signature":           signAPS(cfg, req),
		})
	if err != nil {
		return unreachable(err)
	}
	code := str(body, "response_code")
	if status >= 400 || strings.HasPrefix(code, "0000") == false {
		return failed(code, str(body, "response_message"))
	}
	return Result{
		Status:      "initiated",
		Reference:   str(body, "fort_id"),
		Code:        code,
		Message:     str(body, "response_message"),
		RedirectURL: str(body, "3ds_url"),
	}
}

func (a amazonPS) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	return failed("unsupported",
		unsupported("Amazon Payment Services", "refund").Error())
}

// signAPS builds the SHA signature APS requires.
//
// Their scheme: every request parameter sorted by name, concatenated as
// name=value with no separator, wrapped in the request phrase at both ends,
// then SHA-256. Written out because getting it wrong is a request they refuse
// with a code that does not say why.
func signAPS(cfg config, req ChargeRequest) string {
	fields := map[string]string{
		"access_code":         cfg.settings["access_code"],
		"amount":              fmt.Sprint(minorUnits(req.Amount, req.Currency)),
		"command":             "PURCHASE",
		"currency":            req.Currency,
		"language":            "en",
		"merchant_identifier": cfg.settings["merchant_identifier"],
		"merchant_reference":  req.Reference,
		"return_url":          req.ReturnURL,
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	// Sorted, which is the whole point of the scheme.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	var b strings.Builder
	b.WriteString(cfg.secret)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(fields[k])
	}
	b.WriteString(cfg.secret)
	return sha256Hex(b.String())
}

// --- a card machine on the counter (E3.4) ---------------------------------
//
// Not an acquirer: a terminal on the shop's own network, which the till drives
// over HTTP. The protocol differs by manufacturer, so this speaks the simple
// JSON one the common Saudi terminals expose and reports honestly when it
// cannot reach the machine.

type terminal struct{}

func (terminal) endpoint(cfg config, path string) string {
	address := strings.TrimSpace(cfg.settings["address"])
	if !strings.HasPrefix(address, "http") {
		address = "http://" + address
	}
	return strings.TrimRight(address, "/") + path
}

func (t terminal) Check(ctx context.Context, c *http.Client, cfg config) error {
	if strings.TrimSpace(cfg.settings["address"]) == "" {
		return errs.New(errs.CodeInvalidInput,
			"Give the address the card machine answers on.")
	}
	_, status, err := call(ctx, c, http.MethodGet,
		t.endpoint(cfg, "/status"), nil, nil)
	if err != nil {
		return errs.New(errs.CodeUnavailable,
			"The card machine did not answer. Check it is switched on and on "+
				"the same network as the till.")
	}
	if status >= 400 {
		return errs.Newf(errs.CodeUnavailable,
			"The card machine answered %d.", status)
	}
	return nil
}

func (t terminal) Charge(
	ctx context.Context, c *http.Client, cfg config, req ChargeRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		t.endpoint(cfg, "/sale"), nil, map[string]any{
			"terminalId": cfg.settings["terminal_id"],
			"amount":     minorUnits(req.Amount, req.Currency),
			"currency":   req.Currency,
			"reference":  req.Reference,
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 {
		return failed(str(body, "code"), str(body, "message"))
	}
	// A terminal answers when the customer has finished, so there is no
	// redirect and no pending state: it is captured or it is declined.
	if strings.EqualFold(str(body, "result"), "approved") {
		return Result{
			Status:    "captured",
			Reference: str(body, "rrn"),
			Code:      str(body, "code"),
			Message:   str(body, "message"),
		}
	}
	return failed(str(body, "code"), str(body, "message"))
}

func (t terminal) Refund(
	ctx context.Context, c *http.Client, cfg config, req RefundRequest,
) Result {
	body, status, err := call(ctx, c, http.MethodPost,
		t.endpoint(cfg, "/refund"), nil, map[string]any{
			"terminalId": cfg.settings["terminal_id"],
			"amount":     minorUnits(req.Amount, req.Currency),
			"currency":   req.Currency,
			"reference":  req.Reference,
		})
	if err != nil {
		return unreachable(err)
	}
	if status >= 400 || !strings.EqualFold(str(body, "result"), "approved") {
		return failed(str(body, "code"), str(body, "message"))
	}
	return Result{Status: "refunded", Reference: str(body, "rrn")}
}

// sha256Hex is the digest APS asks for, lowercase hex.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

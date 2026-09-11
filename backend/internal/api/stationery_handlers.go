package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/branding"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/actor"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/httpx"
)

// What a till needs to print a receipt on the shop's own stationery.
//
// The last of I2. The Back Office writes the words (P35) and the invoice screen
// renders them, but the till prints the document a customer actually walks out
// with, and it prints it with no network — so the words have to be on the
// terminal before the network goes.
//
// # One call, resolved from the device
//
// Not `/companies/{id}/templates`, which the back office uses. A till resolves
// its company from its registered device for the same reason it does on every
// other route: a terminal that could name its own company could print another
// company's letterhead, and both belong to the same tenant so row-level
// security would not notice. This returns the seller and the one template a
// receipt uses, together, because a till syncing two calls can end up holding
// half of each.
//
// # No logo, deliberately
//
// `receipt.ts` is 42 columns of plain text, chosen so it prints on every
// counter printer rather than only on the ones whose ESC/POS dialect we
// guessed right. Text cannot hold an image, so the logo is absent from this
// payload rather than sent and ignored — shipping bytes a client would then
// wonder why they never saw is worse than not shipping them.

type stationeryResponse struct {
	// The seller, as the receipt heads itself.
	StoreName string `json:"store_name"`
	VATNumber string `json:"vat_number"`

	// What the shop keeps its books in. Every figure the till shows and every
	// figure it prints is an amount in this, and until now the till was never
	// told: it displayed bare numbers and the receipt carried no code either.
	//
	// Sent with the stationery rather than as its own call because it is the
	// same fact for the same reason — this is what a receipt has to say — and
	// the till already caches this response for use offline.
	BaseCurrency string `json:"base_currency"`

	// The simplified-invoice template: what a counter sale is. The other three
	// document types are back-office documents and no till prints them.
	Header         string `json:"header_text"`
	HeaderAr       string `json:"header_text_ar"`
	Footer         string `json:"footer_text"`
	FooterAr       string `json:"footer_text_ar"`
	ReturnPolicy   string `json:"return_policy"`
	ReturnPolicyAr string `json:"return_policy_ar"`

	ShowTaxNumber bool `json:"show_tax_number"`

	// Where the customer stood and the number they would ring. The BRANCH's,
	// because a chain's head office address is no use to somebody coming back
	// with a return. Empty when the shop has not filled them in.
	StoreAddress string `json:"store_address"`
	StorePhone   string `json:"store_phone"`

	// Whether this shop's receipts carry its mark, and where to fetch it. The
	// image is a separate request on purpose: it is cacheable, it is often
	// absent, and putting half a megabyte of it in a payload the till caches
	// for offline use would be a poor trade.
	ShowLogo  bool      `json:"show_logo"`
	CompanyID uuid.UUID `json:"company_id"`
}

// --- GET /api/v1/pos/stationery -----------------------------------------

func (s *Server) handleTillStationery(w http.ResponseWriter, r *http.Request) {
	a := actor.From(r.Context())

	companyID, err := s.companyFromRequestOrDevice(r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// The branch, so the receipt can print the address and telephone number of
	// the shop the customer actually stood in. Optional, and safe to take from
	// the request: `ReadSeller` joins the store to its company, so an id
	// belonging to somebody else yields no address rather than theirs.
	storeID, _ := optionalUUID(r.URL.Query().Get("store_id"), "store_id")

	scope := branding.Scope{
		TenantID: a.TenantID, CompanyID: companyID, UserID: a.UserID,
	}
	if storeID != nil {
		scope.StoreID = *storeID
	}

	// A counter sale is a simplified invoice, so that is the template a receipt
	// is printed on. Naming it here rather than taking it from the caller: a
	// till has one kind of document and no business choosing another's words.
	template, err := s.branding.Template(r.Context(), scope, "simplified")
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	out := stationeryResponse{
		Header:         template.HeaderText,
		HeaderAr:       template.HeaderTextAr,
		Footer:         template.FooterText,
		FooterAr:       template.FooterTextAr,
		ReturnPolicy:   template.ReturnPolicy,
		ReturnPolicyAr: template.ReturnPolicyAr,
		ShowTaxNumber:  template.ShowTaxNumber,
	}

	seller, err := s.branding.ReadSeller(r.Context(), scope)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	out.StoreName = seller.Name
	out.VATNumber = seller.VATNumber
	out.BaseCurrency = seller.BaseCurrency
	out.StoreAddress = seller.Address
	out.StorePhone = seller.Phone
	out.ShowLogo = template.ShowLogo
	out.CompanyID = companyID

	httpx.JSON(w, http.StatusOK, out)
}

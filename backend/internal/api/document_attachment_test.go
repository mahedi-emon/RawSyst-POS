//go:build integration

// D6, and the two attachment points that the schema allowed and the service
// refused.
//
// `document_entity_valid` is a CHECK constraint naming sixteen kinds of record
// a document may hang off. `entityTables` in internal/docs is the map the
// service uses to find the row. Two of the sixteen were missing from it —
// `company` and `warranty` — so the database accepted them and the service
// answered "Documents cannot be attached to that kind of record" for a kind the
// schema says can.
//
// `company` was the expensive one. The business's own papers are the documents
// a shop is asked for at short notice — the commercial registration, the
// licences, the municipality permit — and none of them could be filed at all.
// The map's own comment said `company` was "checked by a different predicate
// below"; there was no such predicate.
package api

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The business's own registration can be filed against the business.
func TestACompanysOwnPapersCanBeFiled(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")
	company := "?company_id=" + f.companyID.String()

	filed := h.do(t, http.MethodPost, "/api/v1/documents"+company, f.token,
		map[string]any{
			"entity_type": "company",
			// A company's own id IS the company id. Anything else is another
			// business's record and must not be reachable from here.
			"entity_id":      f.companyID.String(),
			"file_name":      "commercial-registration.txt",
			"data":           base64.StdEncoding.EncodeToString([]byte("CR 1010101010")),
			"classification": "licence",
		})
	defer filed.Body.Close()
	if filed.StatusCode != http.StatusOK && filed.StatusCode != http.StatusCreated {
		t.Fatalf("file the registration: %d %s", filed.StatusCode, readBody(t, filed))
	}

	listed := h.do(t, http.MethodGet,
		"/api/v1/documents"+company+"&entity_type=company&entity_id="+
			f.companyID.String(), f.token, nil)
	defer listed.Body.Close()
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list the company's documents: %d", listed.StatusCode)
	}
	page := decodeJSONFrom(t, listed)
	rows, _ := page["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("filed one document against the business, read back %d", len(rows))
	}
}

// Naming another company as the record refuses, and refuses as not-found.
//
// A 403 would confirm the other business exists, which is the thing a
// cross-tenant probe is looking for.
func TestFilingAgainstAnotherBusinessIsRefused(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	refused := h.do(t, http.MethodPost,
		"/api/v1/documents?company_id="+f.companyID.String(), f.token,
		map[string]any{
			"entity_type":    "company",
			"entity_id":      uuid.NewString(),
			"file_name":      "not-ours.txt",
			"data":           base64.StdEncoding.EncodeToString([]byte("x")),
			"classification": "licence",
		})
	defer refused.Body.Close()
	if refused.StatusCode != http.StatusNotFound {
		t.Fatalf("filing against another business answered %d, want 404: %s",
			refused.StatusCode, readBody(t, refused))
	}
}

// Every kind the schema permits can actually be attached to.
//
// This is the test that would have caught both gaps. It reads the CHECK
// constraint rather than repeating it, so a kind added to the schema and
// forgotten in the service fails here instead of reaching somebody who cannot
// file their licence.
func TestEveryPermittedAttachmentKindIsReachable(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	var definition string
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT pg_get_constraintdef(oid)
			FROM pg_constraint
			WHERE conname = 'document_entity_valid'`).Scan(&definition)
	}); err != nil {
		t.Fatalf("read the constraint: %v", err)
	}

	kinds := quotedValues(definition)
	if len(kinds) == 0 {
		t.Fatal("could not read any kinds out of document_entity_valid")
	}

	for _, kind := range kinds {
		// A kind the service cannot place answers "cannot be attached to that
		// kind of record". A kind it CAN place answers not-found for an id
		// that does not exist, which is the right refusal and proves the
		// lookup happened.
		res := h.do(t, http.MethodPost,
			"/api/v1/documents?company_id="+f.companyID.String(), f.token,
			map[string]any{
				"entity_type":    kind,
				"entity_id":      uuid.NewString(),
				"file_name":      "probe.txt",
				"data":           base64.StdEncoding.EncodeToString([]byte("x")),
				"classification": "other",
			})
		body := readBody(t, res)
		res.Body.Close()

		if res.StatusCode == http.StatusBadRequest {
			t.Errorf("the schema permits %q and the service refuses it: %s", kind, body)
		}
	}
}

// quotedValues pulls the 'single quoted' literals out of a constraint
// definition, which is how an IN list is spelled once Postgres has normalised
// it.
func quotedValues(definition string) []string {
	out := []string{}
	inside := false
	current := []rune{}
	for _, r := range definition {
		if r == '\'' {
			if inside {
				out = append(out, string(current))
				current = current[:0]
			}
			inside = !inside
			continue
		}
		if inside {
			current = append(current, r)
		}
	}
	return out
}

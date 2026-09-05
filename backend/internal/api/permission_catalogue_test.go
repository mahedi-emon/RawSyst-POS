//go:build integration

// Every permission the route table enforces has to say what it lets somebody do.
//
// `GET /permissions` is the whole of A6.2's role builder: an owner ticks boxes
// against sentences, not against identifiers. The service falls back to
// `{section: "other", label: <the permission key>}` for anything with no
// catalogue row — which is the right fallback, because a permission that
// suddenly had no description at all should still be grantable rather than
// silently disappearing from a role somebody is editing.
//
// But the fallback is not a place to LIVE. `catalog.edit` and `report.export`
// had been enforced since 0005 and described never, so an owner building a role
// read a hundred and one sentences and then, under a heading called "other",
// the words `catalog.edit` and `report.export` — in Arabic and Bangla too,
// because a fallback has nothing to translate.
//
// Found by rendering the list before building the screen for it. This is what
// stops the next permission arriving the same way.
package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestEveryPermissionSaysWhatItLetsSomebodyDo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/permissions?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read the permissions: %s", readBody(t, resp))
	}
	rows := decodeJSONFrom(t, resp)["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("the role builder is offered no permissions at all")
	}

	var undescribed, untranslated []string
	for _, raw := range rows {
		p := raw.(map[string]any)
		name, _ := p["permission"].(string)
		label, _ := p["label"].(string)
		section, _ := p["section"].(string)

		// The tell. A row that fell through to the fallback is labelled with
		// its own key and filed under "other".
		if label == name || section == "other" {
			undescribed = append(undescribed, name)
			continue
		}
		ar, _ := p["label_ar"].(string)
		bn, _ := p["label_bn"].(string)
		if strings.TrimSpace(ar) == "" || strings.TrimSpace(bn) == "" {
			untranslated = append(untranslated, name)
		}
	}

	if len(undescribed) > 0 {
		t.Errorf("%d permissions are shown to an owner as their own identifier "+
			"rather than as a sentence: %s\nAdd a permission_catalogue row for "+
			"each, the way 0130 did.",
			len(undescribed), strings.Join(undescribed, ", "))
	}
	if len(untranslated) > 0 {
		t.Errorf("%d permissions have an English sentence and no Arabic or "+
			"Bangla one, so the role builder is half-translated: %s",
			len(untranslated), strings.Join(untranslated, ", "))
	}
}

func TestNoPermissionSectionHoldsASingleTickBox(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodGet,
		"/api/v1/permissions?company_id="+f.companyID.String(), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read the permissions: %s", readBody(t, resp))
	}

	counts := map[string]int{}
	for _, raw := range decodeJSONFrom(t, resp)["data"].([]any) {
		section, _ := raw.(map[string]any)["section"].(string)
		counts[section]++
	}

	// A section is a heading on a screen. One with a single member under it
	// reads as a grouping somebody forgot to finish — which is exactly what
	// `inventory`, holding only inventory.recall_batch, was.
	for section, n := range counts {
		if n == 1 {
			t.Errorf("the section %q holds one permission, so the role builder "+
				"draws a heading for a single tick box", section)
		}
	}
}

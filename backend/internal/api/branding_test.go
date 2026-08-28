//go:build integration

// I2 — a client's own logo, over the routes the Back Office calls.
//
// The half of I2 that can honestly be built today: a client sets their own mark
// without anybody editing source. Nothing renders it yet — `receipt.ts` is a
// 42-column plain-text thermal receipt and no A4 or PDF surface exists — so
// these cover storage, validation, isolation and access, which is what the
// feature actually is at this point.
package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// readBodyBytes drains a response as raw bytes. The image routes serve a file,
// not JSON, so the usual helpers do not fit.
func readBodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

// pngOf makes a real PNG of the given size. Real, because the server decides
// what a file is by reading its header — a fixture of random bytes claiming to
// be an image would prove nothing about the check that matters.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x40, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func logoBody(raw []byte) map[string]any {
	return map[string]any{"data": base64.StdEncoding.EncodeToString(raw)}
}

func logoPath(f *shopFixture) string {
	return "/api/v1/companies/" + f.companyID.String() + "/logo"
}

// Upload, read back, replace, remove — the whole loop a client performs.
func TestAClientSetsReplacesAndRemovesTheirOwnLogo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// Nothing set yet. Absent is a state, not a failure: the panel renders
	// empty and offers an upload.
	resp := h.do(t, http.MethodGet, logoPath(f), f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read an unset logo: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	if got := decodeJSON(t, resp)["logo"]; got != nil {
		t.Fatalf("a company with no logo reported %v, want null", got)
	}

	// --- upload ---
	original := pngOf(t, 240, 120)
	resp = h.do(t, http.MethodPut, logoPath(f), f.token, logoBody(original))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	saved := decodeJSON(t, resp)
	if saved["content_type"] != "image/png" {
		t.Errorf("content type = %v, want image/png", saved["content_type"])
	}
	if saved["width"] != float64(240) || saved["height"] != float64(120) {
		t.Errorf("dimensions = %v x %v, want 240 x 120", saved["width"], saved["height"])
	}
	firstChecksum, _ := saved["checksum"].(string)
	if len(firstChecksum) != 64 {
		t.Errorf("checksum = %q, want a sha256 hex digest", firstChecksum)
	}

	// The file comes back byte for byte, under the type the server decided.
	resp = h.do(t, http.MethodGet, logoPath(f)+"/image", f.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch image: status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("served as %q, want image/png", ct)
	}
	if resp.Header.Get("ETag") != `"`+firstChecksum+`"` {
		t.Errorf("ETag = %q, want the checksum", resp.Header.Get("ETag"))
	}
	// A tenant's asset must never sit in a shared cache another tenant reaches.
	if cc := resp.Header.Get("Cache-Control"); cc == "" || !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control = %q, want it marked private", cc)
	}
	body := readBodyBytes(t, resp)
	if !bytes.Equal(body, original) {
		t.Errorf("served %d bytes, uploaded %d; the image was altered",
			len(body), len(original))
	}

	// --- replace ---
	replacement := jpegOf(t, 400, 200)
	resp = h.do(t, http.MethodPut, logoPath(f), f.token, logoBody(replacement))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replace: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	replaced := decodeJSON(t, resp)
	if replaced["content_type"] != "image/jpeg" {
		t.Errorf("after replacing, content type = %v, want image/jpeg",
			replaced["content_type"])
	}
	if replaced["checksum"] == firstChecksum {
		t.Error("replacing the logo did not change the checksum")
	}

	// Replacing REPLACES. One row per company, so the old mark is gone rather
	// than sitting behind the new one.
	var rows int
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM company_logo WHERE company_id = $1`,
			f.companyID).Scan(&rows)
	}); err != nil {
		t.Fatalf("count logos: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the company has %d logo rows after a replace, want exactly 1", rows)
	}

	// --- remove ---
	resp = h.do(t, http.MethodDelete, logoPath(f), f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, logoPath(f), f.token, nil)
	if got := decodeJSON(t, resp)["logo"]; got != nil {
		t.Fatalf("after removing, the logo reads as %v, want null", got)
	}

	// The image route says so plainly rather than serving a stale file.
	resp = h.do(t, http.MethodGet, logoPath(f)+"/image", f.token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("image after removal: status %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// Removing again succeeds: the client asked for no logo, and there is none.
	resp = h.do(t, http.MethodDelete, logoPath(f), f.token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("removing an absent logo: status %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()
}

// What the server refuses, and why each refusal exists.
func TestAnUnusableLogoIsRefusedWithAReason(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	// An SVG is executable markup — it can carry script and fetch external
	// references — so serving one a client uploaded from this origin is stored
	// cross-site scripting. It is refused as a format, not sanitised.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100">` +
		`<script>alert(1)</script></svg>`)

	for _, c := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"an SVG", svg, "could not be read as an image"},
		{"a text file wearing an image name", []byte("this is not an image at all"),
			"could not be read as an image"},
		{"an empty file", []byte{}, "empty"},
		{"an image too small to print", pngOf(t, 16, 16), "at least"},
		{"an image larger than any printer resolves", pngOf(t, 2400, 2400), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := h.do(t, http.MethodPut, logoPath(f), f.token, logoBody(c.raw))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 — %s", resp.StatusCode, readBody(t, resp))
			}
			msg, _ := decodeJSON(t, resp)["error"].(map[string]any)["message"].(string)
			if c.want != "" && !strings.Contains(msg, c.want) {
				t.Errorf("message = %q; it should mention %q", msg, c.want)
			}
			if msg == "" {
				t.Error("the refusal carried no message a client could act on")
			}
		})
	}

	// And nothing was stored by any of them.
	resp := h.do(t, http.MethodGet, logoPath(f), f.token, nil)
	if got := decodeJSON(t, resp)["logo"]; got != nil {
		t.Fatalf("a refused upload left %v behind", got)
	}
}

// The content type is decided by reading the bytes, never by what was claimed.
//
// The route takes no content type from the client at all, which is the point:
// there is nothing to lie with. This proves the server reaches the right answer
// from the file alone, for both accepted formats.
func TestTheContentTypeComesFromTheBytes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	for _, c := range []struct {
		name, want string
		raw        []byte
	}{
		{"png", "image/png", pngOf(t, 64, 64)},
		{"jpeg", "image/jpeg", jpegOf(t, 64, 64)},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := h.do(t, http.MethodPut, logoPath(f), f.token, logoBody(c.raw))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("upload: status %d — %s", resp.StatusCode, readBody(t, resp))
			}
			if got := decodeJSON(t, resp)["content_type"]; got != c.want {
				t.Errorf("content type = %v, want %v", got, c.want)
			}

			resp = h.do(t, http.MethodGet, logoPath(f)+"/image", f.token, nil)
			if got := resp.Header.Get("Content-Type"); got != c.want {
				t.Errorf("served as %q, want %q", got, c.want)
			}
			// The browser is told not to second-guess it either.
			if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Error("the image is served without nosniff")
			}
			resp.Body.Close()
		})
	}
}

// QA gate M8: one client's mark is invisible to another.
//
// The sharpest case for this feature, because a logo is the most recognisable
// thing a business owns — leaking one is not an abstract data-model failure,
// it is one shop seeing another shop's identity.
func TestOneClientCannotSeeAnothersLogo(t *testing.T) {
	h := newHarness(t)
	a := h.seedShop(t, "owner")
	b := h.seedShop(t, "owner")

	secret := pngOf(t, 300, 100)
	resp := h.do(t, http.MethodPut, logoPath(a), a.token, logoBody(secret))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant A upload: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// Tenant B, naming tenant A's company id directly.
	otherPath := "/api/v1/companies/" + a.companyID.String() + "/logo"

	// Not found rather than forbidden: whether another business has set a logo
	// is not this caller's business to learn by probing ids.
	for _, c := range []struct{ name, method, path string }{
		{"metadata", http.MethodGet, otherPath},
		{"the image", http.MethodGet, otherPath + "/image"},
	} {
		resp := h.do(t, c.method, c.path, b.token, nil)
		// The metadata route used to answer 200 with a null logo here, and this
		// test accommodated it on the grounds that null reveals nothing. It
		// revealed one thing: that the route did not check what it was handed.
		// It now refuses the id, so the accommodation is gone with the defect.
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("tenant B reading %s: status %d, want 404 — %s",
				c.name, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// Writing is refused too, and A's mark is untouched by the attempt.
	resp = h.do(t, http.MethodPut, otherPath, b.token, logoBody(pngOf(t, 64, 64)))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tenant B overwriting tenant A's logo: status %d, want 404",
			resp.StatusCode)
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodDelete, otherPath, b.token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tenant B removing tenant A's logo: status %d, want 404",
			resp.StatusCode)
	}
	resp.Body.Close()

	// A's logo is still exactly what A uploaded.
	resp = h.do(t, http.MethodGet, logoPath(a)+"/image", a.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant A re-reading its own logo: status %d", resp.StatusCode)
	}
	if !bytes.Equal(readBodyBytes(t, resp), secret) {
		t.Fatal("tenant A's logo was altered by tenant B's attempts")
	}

	// And the row is invisible at the database, not merely at the route.
	var visible int
	if err := h.pool.TxAsTenant(t.Context(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM company_logo`).Scan(&visible)
	}); err != nil {
		t.Fatalf("query as tenant B: %v", err)
	}
	if visible != 0 {
		t.Fatalf("tenant B can see %d logo rows that are not its own", visible)
	}
}

// QA gate M7: who may change branding, and who may only look at it.
func TestOnlySettingsPermissionChangesTheLogo(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, logoPath(f), f.token, logoBody(pngOf(t, 120, 60)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner upload: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// An auditor reads everything and edits nothing.
	auditor := h.seedUserIn(t, f, "auditor")

	resp = h.do(t, http.MethodGet, logoPath(f), auditor, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auditor reading branding: status %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	for _, c := range []struct{ name, method string }{
		{"replace", http.MethodPut},
		{"remove", http.MethodDelete},
	} {
		var body any
		if c.method == http.MethodPut {
			body = logoBody(pngOf(t, 64, 64))
		}
		resp := h.do(t, c.method, logoPath(f), auditor, body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("auditor trying to %s the logo: status %d, want 403 — %s",
				c.name, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}

	// A cashier holds neither settings verb, so branding is closed to them —
	// but the IMAGE is not, because their till has to print it.
	cashier := h.seedUserIn(t, f, "cashier")

	resp = h.do(t, http.MethodGet, logoPath(f), cashier, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cashier reading branding settings: status %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = h.do(t, http.MethodGet, logoPath(f)+"/image", cashier, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cashier fetching the logo to print: status %d, want 200 — %s",
			resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// A till that already holds this logo is not sent it again.
//
// The image is destined for every receipt, so an unconditional half-megabyte
// per print would be the feature's whole cost. The checksum is the validator.
func TestAnUnchangedLogoIsNotSentTwice(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "owner")

	resp := h.do(t, http.MethodPut, logoPath(f), f.token, logoBody(pngOf(t, 200, 100)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d — %s", resp.StatusCode, readBody(t, resp))
	}
	etagSource, _ := decodeJSON(t, resp)["checksum"].(string)

	resp = h.withHeader(t, http.MethodGet, logoPath(f)+"/image", f.token,
		map[string]string{"If-None-Match": `"` + etagSource + `"`}, nil)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("re-fetch with a matching ETag: status %d, want 304", resp.StatusCode)
	}
	resp.Body.Close()

	// A stale validator gets the image, not a 304.
	resp = h.withHeader(t, http.MethodGet, logoPath(f)+"/image", f.token,
		map[string]string{"If-None-Match": `"0` + etagSource[1:] + `"`}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-fetch with a stale ETag: status %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

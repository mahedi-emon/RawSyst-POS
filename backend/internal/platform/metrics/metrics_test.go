package metrics

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The three properties a metrics endpoint has to have.
//
//   - It counts what happened.
//   - Its cardinality is bounded, so a week of traffic does not produce a
//     million series.
//   - It is not readable by whoever asks.

func TestItCountsWhatHappened(t *testing.T) {
	r := New()
	handler := r.Middleware("/api/v1/sales/{invoiceID}",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))

	for i := 0; i < 3; i++ {
		handler.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/api/v1/sales/abc", nil))
	}

	body := scrape(t, r, "")
	want := `rawsyst_http_requests_total{method="GET",` +
		`route="/api/v1/sales/{invoiceID}",status="4xx"} 3`
	if !strings.Contains(body, want) {
		t.Fatalf("the scrape does not carry %s\n\n%s", want, body)
	}
	if !strings.Contains(body, "rawsyst_http_request_duration_seconds_count") {
		t.Fatal("no duration histogram was written")
	}
}

// TestOneSeriesPerPatternNotPerRecord is the failure this endpoint exists to
// avoid: a label carrying the id turns one route into a series per invoice,
// and the scraper's storage fills up over a weekend.
func TestOneSeriesPerPatternNotPerRecord(t *testing.T) {
	r := New()
	handler := r.Middleware("/api/v1/sales/{invoiceID}",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		handler.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/api/v1/sales/"+id, nil))
	}

	body := scrape(t, r, "")
	if n := strings.Count(body, "rawsyst_http_requests_total{"); n != 1 {
		t.Fatalf("%d request series for five ids, want 1:\n\n%s", n, body)
	}
	for _, id := range []string{`"a"`, `/sales/a`} {
		if strings.Contains(body, id) {
			t.Fatalf("a record id reached a label: %s", id)
		}
	}
}

func TestTheScrapeEndpointIsNotOpenToWhoeverAsks(t *testing.T) {
	r := New()
	handler := r.Handler("a-scrape-token")

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous,
		httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d for no token, want 401", anonymous.Code)
	}

	wrong := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer not-the-token")
	handler.ServeHTTP(wrong, req)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d for the wrong token, want 401", wrong.Code)
	}

	if body := scrape(t, r, "a-scrape-token"); !strings.Contains(body, "rawsyst_up 1") {
		t.Fatalf("the right token did not get a scrape:\n\n%s", body)
	}
}

// TestTwoScrapesOfAnIdleProcessAgree: the output is sorted, so a diff between
// two scrapes shows what moved rather than what Go's map iteration did.
func TestTwoScrapesOfAnIdleProcessAgree(t *testing.T) {
	r := New()
	handler := r.Middleware("/a", http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/a", nil))

	second := r.Middleware("/b", http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {}))
	second.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/b", nil))

	first := lines(scrape(t, r, ""), "rawsyst_http_requests_total{")
	again := lines(scrape(t, r, ""), "rawsyst_http_requests_total{")
	if strings.Join(first, "|") != strings.Join(again, "|") {
		t.Fatalf("two scrapes disagreed on order:\n%v\n%v", first, again)
	}
}

func TestACounterAndAGaugeReachTheScrape(t *testing.T) {
	r := New()
	r.CountBy("rawsyst_jobs_total", 7)
	r.Gauge("rawsyst_db_connections", func() float64 { return 4 })

	body := scrape(t, r, "")
	if !strings.Contains(body, "rawsyst_jobs_total 7") {
		t.Fatalf("the counter is missing:\n\n%s", body)
	}
	if !strings.Contains(body, "rawsyst_db_connections 4") {
		t.Fatalf("the gauge is missing:\n\n%s", body)
	}
}

func scrape(t *testing.T, r *Registry, token string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.Handler(token).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", rec.Code)
	}
	return rec.Body.String()
}

func lines(body, prefix string) []string {
	out := []string{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}
	return out
}

// TestTheWrapperDoesNotBreakAnUpgrade is the trap in every wrapping
// middleware, and it is not hypothetical here: metrics are on by default and
// wrap every route, the live socket included.
//
// An embedded http.ResponseWriter satisfies the interface without carrying the
// optional ones the real writer implements, so a handler that asserts for
// http.Hijacker finds nothing and the socket refuses every connection.
func TestTheWrapperDoesNotBreakAnUpgrade(t *testing.T) {
	var sawHijacker, sawFlusher bool

	handler := New().Middleware("/api/v1/live",
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, sawHijacker = w.(http.Hijacker)
			_, sawFlusher = w.(http.Flusher)
		}))

	// httptest.ResponseRecorder is not a Hijacker, so the assertion has to be
	// made against a writer that is -- otherwise the passthrough would look
	// correct while returning ErrNotSupported.
	handler.ServeHTTP(hijackable{httptest.NewRecorder()},
		httptest.NewRequest(http.MethodGet, "/api/v1/live", nil))

	if !sawHijacker {
		t.Fatal("the handler could not find http.Hijacker: an upgrade would fail")
	}
	if !sawFlusher {
		t.Fatal("the handler could not find http.Flusher: streaming would stall")
	}
}

// hijackable is a ResponseWriter that claims to support hijacking, which is
// what a real server's writer does.
type hijackable struct{ *httptest.ResponseRecorder }

func (hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}

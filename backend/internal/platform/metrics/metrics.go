// Package metrics serves Prometheus text at /metrics.
//
// # Written rather than pulled in
//
// The Prometheus exposition format is documented in about a page: a HELP line,
// a TYPE line, and `name{label="value"} number` for each series. The reference
// client library is excellent and brings protobuf, a registry with its own
// concurrency model, and a process collector that reads /proc — for a product
// that needs four counters and a histogram.
//
// So this is written here. It is about two hundred lines and has one behaviour
// worth arguing about, below.
//
// # Cardinality is capped, and that is the whole design
//
// The way a metrics endpoint kills a service is not CPU: it is a label whose
// values are unbounded. A `path` label carrying the raw URL means one series
// per invoice id, and after a week the endpoint takes a minute to render and
// the scraper's storage is full.
//
// So the request counter is labelled by ROUTE PATTERN — `/api/v1/sales/{id}`,
// the string the router matched, not the string the client sent — and the
// number of patterns is fixed by the route table. Everything else is labelled
// by method and status class. There is no tenant label and no user label: they
// are unbounded by definition, and "which tenant is slow" is a question for
// the logs, which carry the id already.
//
// # What is NOT here
//
// No business metrics: sales per hour, invoices submitted, cash variance. They
// belong in the database, where they are already exact, already per tenant and
// already queryable — and a shop's turnover is not something to publish on a
// scrape endpoint. This measures the SERVICE.
package metrics

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// buckets are the request-duration boundaries, in seconds.
//
// Chosen around what matters at a counter rather than by round numbers: 50ms
// is a fast read, 250ms is a sale committing, 1s is somebody noticing, 5s is a
// cashier apologising to a customer.
var buckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Registry holds every series.
type Registry struct {
	started time.Time

	mu       sync.RWMutex
	requests map[requestKey]*requestSeries

	// inflight is read on every request, so it is an atomic rather than
	// something that takes the map lock.
	inflight atomic.Int64

	// counters are the simple named totals anything in the product can bump:
	// jobs run, notifications sent, sync batches replayed.
	countersMu sync.RWMutex
	counters   map[string]*atomic.Int64

	// gauges are the same, for a value that goes both ways.
	gaugesMu sync.RWMutex
	gauges   map[string]func() float64
}

type requestKey struct {
	method  string
	pattern string
	status  string
}

type requestSeries struct {
	count atomic.Int64
	// sum is seconds as float64 bits, added by compare-and-swap. See addFloat.
	sum    atomic.Uint64
	bucket []atomic.Int64
}

// New builds a registry.
func New() *Registry {
	return &Registry{
		started:  time.Now(),
		requests: map[requestKey]*requestSeries{},
		counters: map[string]*atomic.Int64{},
		gauges:   map[string]func() float64{},
	}
}

// Middleware records every request.
//
// `pattern` is supplied by the caller rather than read from the URL: see the
// package note on cardinality. The router knows which pattern it matched and
// the URL does not.
func (r *Registry) Middleware(
	pattern string, next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.inflight.Add(1)
		defer r.inflight.Add(-1)

		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, req)

		r.observe(req.Method, pattern, recorder.status,
			time.Since(started).Seconds())
	})
}

// statusRecorder remembers what was written so the metric can be labelled by
// it. A handler that never calls WriteHeader wrote a 200, which is why the
// zero value is set rather than left at zero.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// Flush passes through, so a streaming handler keeps streaming.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack passes through, or the WebSocket upgrade fails.
//
// This is the trap in every wrapping middleware. An embedded
// `http.ResponseWriter` satisfies the interface but does NOT carry the optional
// ones the real writer implements, so a handler that type-asserts for
// `http.Hijacker` finds nothing — and the live socket refuses every connection
// with an error about the response writer that names nothing recognisable.
//
// Not hypothetical here: metrics are on by default and wrap every route,
// including the socket.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

// Unwrap is what http.ResponseController follows to reach the real writer, so
// a handler setting a read or write deadline reaches the connection rather than
// this wrapper. The modern half of the same problem Hijack solves.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func (r *Registry) observe(
	method, pattern string, status int, seconds float64,
) {
	key := requestKey{
		method:  method,
		pattern: pattern,
		// The class, not the code. 404 and 409 are both "the client asked for
		// something that is not there", and one series per code multiplies the
		// count by twenty for no question anybody asks of a graph.
		status: statusClass(status),
	}

	r.mu.RLock()
	series, ok := r.requests[key]
	r.mu.RUnlock()

	if !ok {
		r.mu.Lock()
		series, ok = r.requests[key]
		if !ok {
			series = &requestSeries{bucket: make([]atomic.Int64, len(buckets))}
			r.requests[key] = series
		}
		r.mu.Unlock()
	}

	series.count.Add(1)
	addFloat(&series.sum, seconds)
	for i, upper := range buckets {
		if seconds <= upper {
			series.bucket[i].Add(1)
		}
	}
}

func statusClass(status int) string {
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	}
	return "5xx"
}

// addFloat adds to a float64 held in a uint64, by compare-and-swap.
//
// The alternative is a mutex per series, which is a lock taken on every
// request on the hot path for the sake of one addition.
func addFloat(target *atomic.Uint64, delta float64) {
	for {
		old := target.Load()
		next := float64FromBits(old) + delta
		if target.CompareAndSwap(old, float64ToBits(next)) {
			return
		}
	}
}

// Count adds one to a named total, creating it on first use.
func (r *Registry) Count(name string) {
	r.CountBy(name, 1)
}

// CountBy adds to a named total.
func (r *Registry) CountBy(name string, n int64) {
	r.countersMu.RLock()
	c, ok := r.counters[name]
	r.countersMu.RUnlock()
	if !ok {
		r.countersMu.Lock()
		c, ok = r.counters[name]
		if !ok {
			c = &atomic.Int64{}
			r.counters[name] = c
		}
		r.countersMu.Unlock()
	}
	c.Add(n)
}

// Gauge registers a value read at scrape time.
//
// A function rather than a number, so "how many connections are open" is
// answered by asking the pool when somebody looks rather than by every caller
// remembering to update a counter.
func (r *Registry) Gauge(name string, read func() float64) {
	r.gaugesMu.Lock()
	r.gauges[name] = read
	r.gaugesMu.Unlock()
}

// Handler serves the exposition format.
//
// `token` guards it. Empty means unguarded, which config refuses outside
// development: the endpoint describes how much business every shop on the
// stack is doing.
func (r *Registry) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if token != "" && !authorised(req, token) {
			// No WWW-Authenticate: this is not a browser destination and a
			// prompt would only invite somebody to guess.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder
		r.write(&b)
		_, _ = w.Write([]byte(b.String()))
	})
}

func authorised(req *http.Request, token string) bool {
	header := req.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) ||
		!strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	// Constant-time is deliberate even here: a scrape token is a credential,
	// and a timing oracle on an endpoint that answers a thousand times an hour
	// is a real one.
	return constantTimeEqual(header[len(prefix):], token)
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func (r *Registry) write(b *strings.Builder) {
	fmt.Fprintf(b, "# HELP rawsyst_up 1 while the process is serving.\n")
	fmt.Fprintf(b, "# TYPE rawsyst_up gauge\n")
	fmt.Fprintf(b, "rawsyst_up 1\n")

	fmt.Fprintf(b, "# HELP rawsyst_uptime_seconds Seconds since start-up.\n")
	fmt.Fprintf(b, "# TYPE rawsyst_uptime_seconds gauge\n")
	fmt.Fprintf(b, "rawsyst_uptime_seconds %g\n", time.Since(r.started).Seconds())

	fmt.Fprintf(b, "# HELP rawsyst_requests_in_flight Requests being served now.\n")
	fmt.Fprintf(b, "# TYPE rawsyst_requests_in_flight gauge\n")
	fmt.Fprintf(b, "rawsyst_requests_in_flight %d\n", r.inflight.Load())

	r.writeRequests(b)
	r.writeCounters(b)
	r.writeGauges(b)
	writeRuntime(b)
}

func (r *Registry) writeRequests(b *strings.Builder) {
	r.mu.RLock()
	keys := make([]requestKey, 0, len(r.requests))
	for k := range r.requests {
		keys = append(keys, k)
	}
	series := make(map[requestKey]*requestSeries, len(r.requests))
	for k, v := range r.requests {
		series[k] = v
	}
	r.mu.RUnlock()

	// Sorted, so two scrapes of an unchanged process are byte-identical and a
	// diff of them shows what moved rather than what Go's map iteration did.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pattern != keys[j].pattern {
			return keys[i].pattern < keys[j].pattern
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})

	if len(keys) == 0 {
		return
	}

	fmt.Fprintf(b, "# HELP rawsyst_http_requests_total Requests served, by "+
		"route pattern and status class.\n")
	fmt.Fprintf(b, "# TYPE rawsyst_http_requests_total counter\n")
	for _, k := range keys {
		fmt.Fprintf(b, "rawsyst_http_requests_total{method=%q,route=%q,status=%q} %d\n",
			k.method, k.pattern, k.status, series[k].count.Load())
	}

	fmt.Fprintf(b, "# HELP rawsyst_http_request_duration_seconds How long "+
		"requests took.\n")
	fmt.Fprintf(b, "# TYPE rawsyst_http_request_duration_seconds histogram\n")
	for _, k := range keys {
		s := series[k]
		total := s.count.Load()
		for i, upper := range buckets {
			fmt.Fprintf(b,
				"rawsyst_http_request_duration_seconds_bucket"+
					"{method=%q,route=%q,status=%q,le=\"%g\"} %d\n",
				k.method, k.pattern, k.status, upper, s.bucket[i].Load())
		}
		fmt.Fprintf(b,
			"rawsyst_http_request_duration_seconds_bucket"+
				"{method=%q,route=%q,status=%q,le=\"+Inf\"} %d\n",
			k.method, k.pattern, k.status, total)
		fmt.Fprintf(b,
			"rawsyst_http_request_duration_seconds_sum"+
				"{method=%q,route=%q,status=%q} %g\n",
			k.method, k.pattern, k.status,
			float64FromBits(s.sum.Load()))
		fmt.Fprintf(b,
			"rawsyst_http_request_duration_seconds_count"+
				"{method=%q,route=%q,status=%q} %d\n",
			k.method, k.pattern, k.status, total)
	}
}

func (r *Registry) writeCounters(b *strings.Builder) {
	r.countersMu.RLock()
	names := make([]string, 0, len(r.counters))
	values := make(map[string]int64, len(r.counters))
	for name, c := range r.counters {
		names = append(names, name)
		values[name] = c.Load()
	}
	r.countersMu.RUnlock()
	sort.Strings(names)

	for _, name := range names {
		fmt.Fprintf(b, "# TYPE %s counter\n%s %d\n", name, name, values[name])
	}
}

func (r *Registry) writeGauges(b *strings.Builder) {
	r.gaugesMu.RLock()
	names := make([]string, 0, len(r.gauges))
	reads := make(map[string]func() float64, len(r.gauges))
	for name, read := range r.gauges {
		names = append(names, name)
		reads[name] = read
	}
	r.gaugesMu.RUnlock()
	sort.Strings(names)

	for _, name := range names {
		fmt.Fprintf(b, "# TYPE %s gauge\n%s %g\n", name, name, reads[name]())
	}
}

// writeRuntime is the Go process itself.
//
// Enough to answer "is it leaking" and "is it garbage collecting constantly",
// which are the two questions a Go service gets asked at three in the morning.
func writeRuntime(b *strings.Builder) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Fprintf(b, "# TYPE rawsyst_go_goroutines gauge\n")
	fmt.Fprintf(b, "rawsyst_go_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(b, "# TYPE rawsyst_go_heap_bytes gauge\n")
	fmt.Fprintf(b, "rawsyst_go_heap_bytes %d\n", mem.HeapAlloc)
	fmt.Fprintf(b, "# TYPE rawsyst_go_heap_objects gauge\n")
	fmt.Fprintf(b, "rawsyst_go_heap_objects %d\n", mem.HeapObjects)
	fmt.Fprintf(b, "# TYPE rawsyst_go_gc_total counter\n")
	fmt.Fprintf(b, "rawsyst_go_gc_total %d\n", mem.NumGC)
	fmt.Fprintf(b, "# TYPE rawsyst_go_gc_pause_seconds_total counter\n")
	fmt.Fprintf(b, "rawsyst_go_gc_pause_seconds_total %g\n",
		float64(mem.PauseTotalNs)/1e9)
}

// An atomic float64 is a uint64 holding its bits. Named here so the
// compare-and-swap above reads as arithmetic rather than as bit fiddling.
func float64ToBits(f float64) uint64   { return math.Float64bits(f) }
func float64FromBits(u uint64) float64 { return math.Float64frombits(u) }

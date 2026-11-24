package query

import (
	"context"
	"errors"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"syscon-bench/internal/metrics"
	"syscon-bench/internal/storage"
)

func TestClassBuckets(t *testing.T) {
	if classFor(0) != storage.QueryFullText || classFor(24) != storage.QueryFullText {
		t.Fatal("fulltext")
	}
	if classFor(25) != storage.QueryExact || classFor(49) != storage.QueryExact {
		t.Fatal("exact")
	}
	if classFor(50) != storage.QueryFilter || classFor(74) != storage.QueryFilter {
		t.Fatal("filter")
	}
	if classFor(75) != storage.QueryAggregation || classFor(99) != storage.QueryAggregation {
		t.Fatal("aggregation")
	}
}

func TestFilterWindow(t *testing.T) {
	runStart := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	first := runStart.Add(5 * time.Second)

	now := runStart.Add(20 * time.Second)
	from, to := filterWindow(now, runStart, first)
	if !from.Equal(first) || !to.Equal(now) {
		t.Fatalf("young %s %s", from, to)
	}

	now = runStart.Add(59 * time.Second)
	from, to = filterWindow(now, runStart, first)
	if !from.Equal(first) || !to.Equal(now) {
		t.Fatalf("59s %s %s", from, to)
	}

	now = runStart.Add(60 * time.Second)
	from, _ = filterWindow(now, runStart, first)
	if !from.Equal(now.Add(-filterLookback)) {
		t.Fatalf("exactly 60s raised to %s", from)
	}

	now = runStart.Add(2 * time.Minute)
	from, to = filterWindow(now, runStart, first)
	if !from.Equal(now.Add(-filterLookback)) || !to.Equal(now) {
		t.Fatalf("old %s %s", from, to)
	}

	now = runStart.Add(10 * time.Second)
	from, _ = filterWindow(now, runStart, time.Time{})
	if !from.Equal(now.Add(-filterLookback)) {
		t.Fatalf("missing first %s", from)
	}
}

func TestMakeQueryPredicates(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 1))
	runStart := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	now := runStart.Add(10 * time.Second)
	snap := snapshot{
		tokens:   []string{"ErrorCode", "checkout"},
		service:  "checkout",
		traceID:  "0123456789abcdef0123456789abcdef",
		uuid:     "550e8400-e29b-41d4-a716-446655440000",
		status:   "500",
		severity: "error",
		first:    runStart.Add(time.Second),
	}
	seenExact := map[string]string{}
	seenFilter := map[string]string{}
	for i := 0; i < 200; i++ {
		q, ok := makeQuery(rng, storage.QueryFullText, snap, runStart, now)
		if !ok || (q.Token != "ErrorCode" && q.Token != "checkout") || q.Field != "" || !q.From.IsZero() {
			t.Fatalf("fulltext %+v", q)
		}
		q, ok = makeQuery(rng, storage.QueryExact, snap, runStart, now)
		if !ok || !q.From.IsZero() || !q.To.IsZero() || q.Value != snap.value(q.Field) {
			t.Fatalf("exact %+v", q)
		}
		seenExact[q.Field] = q.Value
		q, ok = makeQuery(rng, storage.QueryFilter, snap, runStart, now)
		if !ok || !q.From.Equal(snap.first) || !q.To.Equal(now) || q.Value != snap.value(q.Field) || q.Service != snap.service || q.Token != "" {
			t.Fatalf("filter %+v", q)
		}
		seenFilter[q.Field] = q.Value
	}
	for _, field := range exactFields {
		if seenExact[field] == "" {
			t.Fatalf("exact missed %s in %v", field, seenExact)
		}
	}
	if seenExact["status-code"] != "500" || seenExact["trace-id"] != snap.traceID {
		t.Fatalf("exact values %v", seenExact)
	}
	for _, field := range filterFields {
		if seenFilter[field] == "" {
			t.Fatalf("filter missed %s in %v", field, seenFilter)
		}
	}
	if _, ok := makeQuery(rng, storage.QueryFullText, snapshot{}, runStart, now); ok {
		t.Fatal("fulltext without tokens")
	}
}

func TestExecuteMixAndSkip(t *testing.T) {
	recent := NewRecent(8)
	recent.Add([]storage.Event{acceptedEvent("ab cd", "2026-11-02T00:00:01.000Z")})
	recent.Add([]storage.Event{acceptedEvent("checkout info request completed", "2026-11-02T00:00:02.000Z")})
	q := &captureQuerier{result: storage.Result{Hits: 3, Body: []byte("[]")}}
	obs := &recordObs{}
	client := &Client{
		Querier: q,
		Recent:  recent,
		Metrics: obs,
		Logger:  log.New(io.Discard, "", 0),
		Seed:    1,
		Timeout: time.Second,
	}
	runStart := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	now := runStart.Add(30 * time.Second)
	const n = 3000
	for i := 0; i < n; i++ {
		client.Execute(context.Background(), runStart, now)
	}
	got := q.snapshot()
	if len(got) != n {
		t.Fatalf("searches %d", len(got))
	}
	counts := map[storage.QueryClass]int{}
	for _, query := range got {
		counts[query.Class]++
		switch query.Class {
		case storage.QueryFullText:
			if query.Token != "checkout" && query.Token != "info" && query.Token != "request" && query.Token != "completed" {
				t.Fatalf("token %q", query.Token)
			}
			if !query.From.IsZero() || query.Field != "" {
				t.Fatalf("%+v", query)
			}
		case storage.QueryExact:
			if query.Value != fieldOf(acceptedEvent("", ""), query.Field) || !query.From.IsZero() {
				t.Fatalf("%+v", query)
			}
		case storage.QueryFilter:
			if query.Value != fieldOf(acceptedEvent("", ""), query.Field) || !query.From.Before(query.To) {
				t.Fatalf("%+v", query)
			}
		case storage.QueryAggregation:
			if query.Field != "" || query.Value != "" || !query.From.Before(query.To) {
				t.Fatalf("%+v", query)
			}
		default:
			t.Fatalf("class %s", query.Class)
		}
	}
	for _, class := range []storage.QueryClass{storage.QueryFullText, storage.QueryExact, storage.QueryFilter, storage.QueryAggregation} {
		if counts[class] < n*20/100 || counts[class] > n*30/100 {
			t.Fatalf("%s %d", class, counts[class])
		}
	}
	if len(obs.calls) != n {
		t.Fatalf("observed %d", len(obs.calls))
	}

	empty := &Client{Querier: q, Recent: NewRecent(4), Logger: log.New(io.Discard, "", 0), Seed: 1}
	before := len(q.snapshot())
	empty.Execute(context.Background(), runStart, now)
	if len(q.snapshot()) != before {
		t.Fatal("empty recent searched")
	}
	if len(obs.calls) != n {
		t.Fatal("skip recorded a metric")
	}
}

func TestRunRecordsLatencyAndErrors(t *testing.T) {
	reg := prometheus.NewRegistry()
	series, err := metrics.New(reg, reg, "blackhole", "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer series.Unregister()
	var buf strings.Builder
	c := &Client{
		Metrics: series,
		Logger:  log.New(&buf, "", 0),
		Timeout: time.Second,
	}
	c.Querier = querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
		time.Sleep(30 * time.Millisecond)
		return storage.Result{Hits: 4, Body: []byte("ok")}, nil
	})
	c.run(context.Background(), storage.Query{Class: storage.QueryFullText, Token: "checkout"})
	if !strings.Contains(buf.String(), "class=fulltext") || !strings.Contains(buf.String(), "hits=4") {
		t.Fatalf("log %q", buf.String())
	}

	c.Querier = querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
		return storage.Result{Hits: 1, Body: []byte("no")}, &storage.StatusError{Status: 503}
	})
	c.run(context.Background(), storage.Query{Class: storage.QueryFullText, Token: "checkout"})

	c.Querier = querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
		return storage.Result{}, context.DeadlineExceeded
	})
	c.run(context.Background(), storage.Query{Class: storage.QueryExact, Field: "service", Value: "checkout"})

	c.Querier = querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
		return storage.Result{}, timeoutErr{}
	})
	c.run(context.Background(), storage.Query{Class: storage.QueryExact, Field: "service", Value: "checkout"})

	c.Querier = querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
		return storage.Result{}, errors.New("connection refused")
	})
	c.run(context.Background(), storage.Query{Class: storage.QueryFilter, Field: "service", Value: "checkout"})

	// A canceled call is not a query error, even when Search returns the cancel.
	c.Querier = querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
		return storage.Result{}, context.Canceled
	})
	c.run(context.Background(), storage.Query{Class: storage.QueryFilter, Field: "service", Value: "checkout"})

	text := mustText(t, series)
	if got := sampleValue(text, "bench_query_duration_seconds_count", "fulltext"); got != "2" {
		t.Fatalf("fulltext histogram %q\n%s", got, text)
	}
	if got := sampleValue(text, "bench_query_errors_total", "fulltext"); got != "1" {
		t.Fatalf("fulltext errors %q\n%s", got, text)
	}
	if got := sampleValue(text, "bench_query_duration_seconds_count", "exact"); got != "0" {
		t.Fatalf("exact histogram %q\n%s", got, text)
	}
	if got := sampleValue(text, "bench_query_errors_total", "exact"); got != "2" {
		t.Fatalf("exact errors %q\n%s", got, text)
	}
	if got := sampleValue(text, "bench_query_errors_total", "filter"); got != "1" {
		t.Fatalf("filter errors %q\n%s", got, text)
	}
	if got := sampleValue(text, "bench_query_duration_seconds_count", "filter"); got != "0" {
		t.Fatalf("filter histogram %q\n%s", got, text)
	}
	if !strings.Contains(buf.String(), "hits=1") || !strings.Contains(buf.String(), "status 503") {
		t.Fatalf("status log %q", buf.String())
	}
}

func TestClientTimeoutAndBlackhole(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hang.Close()
	bh, err := storage.NewBlackhole(hang.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	obs := &recordObs{}
	c := &Client{Querier: bh, Metrics: obs, Logger: log.New(io.Discard, "", 0), Timeout: 40 * time.Millisecond}
	c.run(context.Background(), storage.Query{Class: storage.QueryExact, Field: "uuid", Value: "abc"})
	if len(obs.calls) != 1 || obs.calls[0].bodyRead || !obs.calls[0].failed || obs.calls[0].class != "exact" {
		t.Fatalf("%+v", obs.calls)
	}

	okSrv := httptest.NewServer(storage.Handler("", ""))
	defer okSrv.Close()
	bh, err = storage.NewBlackhole(okSrv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	obs.calls = nil
	var logBuf strings.Builder
	c.Querier = bh
	c.Logger = log.New(&logBuf, "", 0)
	c.Timeout = time.Second
	start := time.Now()
	c.run(context.Background(), storage.Query{Class: storage.QueryFullText, Token: "checkout"})
	if time.Since(start) > 2*time.Second {
		t.Fatal("search hung")
	}
	if len(obs.calls) != 1 || !obs.calls[0].bodyRead || obs.calls[0].failed || obs.calls[0].seconds <= 0 {
		t.Fatalf("%+v", obs.calls)
	}
	if !strings.Contains(logBuf.String(), "hits=0") {
		t.Fatalf("log %q", logBuf.String())
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer bad.Close()
	bh, err = storage.NewBlackhole(bad.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	obs.calls = nil
	c.Querier = bh
	c.run(context.Background(), storage.Query{Class: storage.QueryFilter, Field: "service", Value: "checkout"})
	if len(obs.calls) != 1 || !obs.calls[0].bodyRead || !obs.calls[0].failed {
		t.Fatalf("%+v", obs.calls)
	}
}

func TestLatencyIncludesSearch(t *testing.T) {
	obs := &recordObs{}
	c := &Client{
		Metrics: obs,
		Logger:  log.New(io.Discard, "", 0),
		Querier: querierFunc(func(context.Context, storage.Query) (storage.Result, error) {
			time.Sleep(40 * time.Millisecond)
			return storage.Result{Hits: 1, Body: []byte("x")}, nil
		}),
	}
	c.run(context.Background(), storage.Query{Class: storage.QueryExact, Field: "service", Value: "checkout"})
	if len(obs.calls) != 1 || obs.calls[0].seconds < 0.02 {
		t.Fatalf("%+v", obs.calls)
	}
}

func acceptedEvent(message, at string) storage.Event {
	if message == "" {
		message = "checkout info request completed"
	}
	if at == "" {
		at = "2026-11-02T00:00:01.000Z"
	}
	return storage.Event{
		Message:     message,
		Service:     "checkout",
		TraceID:     "0123456789abcdef0123456789abcdef",
		UUID:        "550e8400-e29b-41d4-a716-446655440000",
		StatusCode:  200,
		LogSeverity: "info",
		Time:        at,
	}
}

func fieldOf(ev storage.Event, field string) string {
	switch field {
	case "service":
		return ev.Service
	case "trace-id":
		return ev.TraceID
	case "uuid":
		return ev.UUID
	case "status-code":
		return "200"
	case "log-severity":
		return ev.LogSeverity
	default:
		return ""
	}
}

type captureQuerier struct {
	mu     sync.Mutex
	got    []storage.Query
	result storage.Result
	err    error
}

func (c *captureQuerier) Search(_ context.Context, q storage.Query) (storage.Result, error) {
	c.mu.Lock()
	c.got = append(c.got, q)
	c.mu.Unlock()
	return c.result, c.err
}

func (c *captureQuerier) snapshot() []storage.Query {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]storage.Query, len(c.got))
	copy(out, c.got)
	return out
}

type querierFunc func(context.Context, storage.Query) (storage.Result, error)

func (f querierFunc) Search(ctx context.Context, q storage.Query) (storage.Result, error) {
	return f(ctx, q)
}

type recordObs struct {
	mu    sync.Mutex
	calls []obsCall
}

type obsCall struct {
	class    string
	seconds  float64
	bodyRead bool
	failed   bool
}

func (o *recordObs) ObserveQuery(class string, seconds float64, bodyRead, failed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, obsCall{class, seconds, bodyRead, failed})
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func mustText(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	b, err := m.Text()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sampleValue(text, name, class string) string {
	prefix := name + "{"
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if class != "" && !strings.Contains(line, `query_class="`+class+`"`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		return fields[len(fields)-1]
	}
	return ""
}

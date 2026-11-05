package metrics

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestSeriesText(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Name: "other_total", Help: "ignored"}))
	m, err := New(reg, reg, "blackhole", "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unregister()
	m.IngestAccepted(3, 30)
	m.IngestFailed(2)
	m.ObserveQuery("fulltext", 0.02, true, true)
	body, err := m.Text()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, name := range []string{
		"bench_ingest_events_total",
		"bench_ingest_bytes_total",
		"bench_ingest_errors_total",
		"bench_query_duration_seconds",
		"bench_query_errors_total",
	} {
		if !strings.Contains(text, name) {
			t.Fatalf("missing %s\n%s", name, text)
		}
	}
	if strings.Contains(text, "other_total") {
		t.Fatalf("unexpected series\n%s", text)
	}
	if !strings.Contains(text, `scenario="demo"`) || !strings.Contains(text, `storage="blackhole"`) {
		t.Fatalf("labels\n%s", text)
	}
	if !strings.Contains(text, `query_class="fulltext"`) || !strings.Contains(text, `le="0.005"`) || !strings.Contains(text, `le="60"`) {
		t.Fatalf("histogram\n%s", text)
	}
	if got := lineValue(text, "bench_ingest_events_total"); got != "3" {
		t.Fatalf("events %q\n%s", got, text)
	}
	if got := lineValue(text, "bench_ingest_bytes_total"); got != "30" {
		t.Fatalf("bytes %q", got)
	}
	if got := lineValue(text, "bench_ingest_errors_total"); got != "2" {
		t.Fatalf("errors %q", got)
	}
}

func TestPush(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := New(reg, reg, "blackhole", "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unregister()
	m.IngestAccepted(4, 64)
	var body, ctype, user, pass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		u, p, _ := r.BasicAuth()
		user, pass = u, p
		ctype = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := Push(context.Background(), nil, srv.URL, "admin", "admin", m); err != nil {
		t.Fatal(err)
	}
	if ctype != ContentType {
		t.Fatalf("content-type %q", ctype)
	}
	if user != "admin" || pass != "admin" {
		t.Fatalf("auth %s/%s", user, pass)
	}
	if !strings.Contains(body, "bench_ingest_events_total") || !strings.Contains(body, "4") {
		t.Fatalf("body %s", body)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer bad.Close()
	var logBuf strings.Builder
	p := &Pusher{
		Endpoint: bad.URL,
		Metrics:  m,
		Logger:   log.New(&logBuf, "", 0),
	}
	before := lineValue(mustText(t, m), "bench_ingest_errors_total")
	p.At(context.Background(), true)
	if !strings.Contains(logBuf.String(), "dashboard push") {
		t.Fatalf("log %q", logBuf.String())
	}
	if got := lineValue(mustText(t, m), "bench_ingest_errors_total"); got != before {
		t.Fatalf("push changed ingest errors %q -> %q", before, got)
	}
	if PushInterval != 15*time.Second {
		t.Fatalf("interval %s", PushInterval)
	}
}

func mustText(t *testing.T, m *Metrics) string {
	t.Helper()
	b, err := m.Text()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lineValue(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, name) || strings.HasPrefix(line, name+"{") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			return fields[len(fields)-1]
		}
	}
	return ""
}

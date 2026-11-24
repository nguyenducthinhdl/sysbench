package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestLokiPushAndSearch(t *testing.T) {
	var mu sync.Mutex
	var user, pass string
	var pushed []byte
	var queries []string
	var eval string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		user, pass = u, p
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/push"):
			mu.Lock()
			pushed = body
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			q, _ := url.QueryUnescape(r.URL.RawQuery)
			mu.Lock()
			queries = append(queries, q)
			eval = r.URL.Query().Get("time")
			mu.Unlock()
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"3"]}]}}`)
		}
	}))
	defer srv.Close()

	loki, err := NewLoki(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	ev := sampleEvent()
	ev.Message = "checkout info\n\tat checkout.api"
	if err := loki.Push(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := loki.Push(context.Background(), []Event{ev}); err != nil {
		t.Fatal(err)
	}
	res, err := loki.Search(context.Background(), Query{Class: QueryFullText, Token: `a"b`})
	if err != nil || res.Hits != 3 {
		t.Fatalf("hits %d err %v", res.Hits, err)
	}
	if _, err := loki.Search(context.Background(), Query{Class: QueryExact, Field: "trace-id", Value: ev.TraceID}); err != nil {
		t.Fatal(err)
	}
	if _, err := loki.Search(context.Background(), filterQuery()); err != nil {
		t.Fatal(err)
	}
	agg := filterQuery()
	agg.Class = QueryAggregation
	if _, err = loki.Search(context.Background(), agg); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if user != "admin" || pass != "admin" {
		t.Fatalf("auth %s/%s", user, pass)
	}
	var payload struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][]any           `json:"values"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(pushed, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Streams) != 1 {
		t.Fatalf("%d streams", len(payload.Streams))
	}
	labels := payload.Streams[0].Stream
	if labels["job"] != "syscon-bench" || labels["service"] != "checkout" || labels["severity"] != "info" || labels["component"] != "api" {
		t.Fatalf("labels %+v", labels)
	}
	values := payload.Streams[0].Values
	if len(values) != 1 || values[0][1] != ev.Message {
		t.Fatalf("values %+v", values)
	}
	meta, ok := values[0][2].(map[string]any)
	if !ok || meta["trace_id"] != ev.TraceID || meta["status_code"] != "200" || meta["uuid"] != ev.UUID {
		t.Fatalf("meta %+v", values[0][2])
	}
	if len(queries) != 4 {
		t.Fatalf("queries %d", len(queries))
	}
	if !strings.Contains(queries[0], `|= "a\"b"`) || !strings.Contains(queries[0], "count_over_time") {
		t.Fatalf("fulltext %s", queries[0])
	}
	if !strings.Contains(queries[1], `trace_id="`+ev.TraceID+`"`) {
		t.Fatalf("exact %s", queries[1])
	}
	if !strings.Contains(queries[2], `service="checkout"`) || !strings.Contains(queries[2], "[60s]") || eval == "" {
		t.Fatalf("filter %s time %s", queries[2], eval)
	}
	if !strings.Contains(queries[3], "bytes_over_time") || !strings.Contains(queries[3], "sum(") {
		t.Fatalf("aggregation %s", queries[3])
	}
}

func TestLokiStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()
	loki, err := NewLoki(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = loki.Push(context.Background(), []Event{sampleEvent()})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("%v", err)
	}
	res, err := loki.Search(context.Background(), Query{Class: QueryExact, Field: "uuid", Value: "abc"})
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusBadRequest {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(string(res.Body), "nope") {
		t.Fatalf("%q", res.Body)
	}
	if _, err := loki.Search(context.Background(), Query{Class: QueryExact, Field: "component", Value: "api"}); err == nil {
		t.Fatal("expected field error")
	}
}

func TestNewLokiRejectsRelativeURL(t *testing.T) {
	if _, err := NewLoki("http://", "", ""); err == nil {
		t.Fatal("expected error")
	}
}

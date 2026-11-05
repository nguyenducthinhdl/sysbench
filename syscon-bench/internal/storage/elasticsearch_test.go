package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestElasticsearchPushAndSearch(t *testing.T) {
	var mu sync.Mutex
	var user, pass, bulkType string
	var template, bulk string
	var searches []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		user, pass = u, p
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "_index_template"):
			template = string(body)
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			bulkType = r.Header.Get("Content-Type")
			bulk = string(body)
			_, _ = io.WriteString(w, `{"errors":false,"items":[{"create":{"status":201}}]}`)
			mu.Unlock()
			return
		case strings.HasSuffix(r.URL.Path, "/_search"):
			searches = append(searches, string(body))
			_, _ = io.WriteString(w, `{"hits":{"total":{"value":6,"relation":"eq"}}}`)
			mu.Unlock()
			return
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	es, err := NewElasticsearch(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	ev := sampleEvent()
	if err := es.Push(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := es.Push(context.Background(), []Event{ev}); err != nil {
		t.Fatal(err)
	}
	res, err := es.Search(context.Background(), Query{Class: QueryFullText, Token: "checkout"})
	if err != nil || res.Hits != 6 {
		t.Fatalf("hits %d err %v", res.Hits, err)
	}
	if _, err := es.Search(context.Background(), Query{Class: QueryExact, Field: "status-code", Value: "500"}); err != nil {
		t.Fatal(err)
	}
	if _, err := es.Search(context.Background(), filterQuery()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if user != "admin" || pass != "admin" || bulkType != "application/x-ndjson" {
		t.Fatalf("auth %s/%s type %s", user, pass, bulkType)
	}
	if !strings.Contains(template, `"index.mode": "logsdb"`) || !strings.Contains(template, `"host.name"`) || !strings.Contains(template, `"@timestamp"`) {
		t.Fatalf("template %s", template)
	}
	if !strings.Contains(bulk, `"create"`) || !strings.Contains(bulk, ev.UUID) || !strings.Contains(bulk, `"@timestamp"`) || !strings.Contains(bulk, `"name":"host-0000"`) {
		t.Fatalf("bulk %s", bulk)
	}
	if len(searches) != 3 {
		t.Fatalf("searches %d", len(searches))
	}
	if !strings.Contains(searches[0], `"match"`) || !strings.Contains(searches[0], "checkout") {
		t.Fatalf("fulltext %s", searches[0])
	}
	var exact struct {
		Query struct {
			Term map[string]any `json:"term"`
		} `json:"query"`
	}
	if err := json.Unmarshal([]byte(searches[1]), &exact); err != nil {
		t.Fatal(err)
	}
	if exact.Query.Term["status-code"] != float64(500) {
		t.Fatalf("term %+v", exact.Query.Term)
	}
	if !strings.Contains(searches[2], `"@timestamp"`) || !strings.Contains(searches[2], `"gte"`) {
		t.Fatalf("filter %s", searches[2])
	}
}

func TestElasticsearchBulkErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_index_template") {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, `{"errors":true,"items":[{"create":{"status":400,"error":{"type":"mapper_parsing_exception"}}}]}`)
	}))
	defer srv.Close()
	es, err := NewElasticsearch(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = es.Push(context.Background(), []Event{sampleEvent()})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("%v", err)
	}
}

func TestElasticsearchStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_index_template") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	es, err := NewElasticsearch(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := es.Search(context.Background(), Query{Class: QueryExact, Field: "uuid", Value: "abc"})
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusServiceUnavailable {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(string(res.Body), "nope") {
		t.Fatalf("%q", res.Body)
	}
}

func TestNewElasticsearchRejectsRelativeURL(t *testing.T) {
	if _, err := NewElasticsearch("logs", "", ""); err == nil {
		t.Fatal("expected error")
	}
}

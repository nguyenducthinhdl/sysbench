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

func TestClickHousePushAndSearch(t *testing.T) {
	var mu sync.Mutex
	var user, pass, insertType, insertQuery string
	var inserted []Event
	var queries []string
	var params []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		user, pass = u, p
		mu.Unlock()
		sql := r.URL.Query().Get("query")
		if sql == "" {
			sql = string(body)
		}
		switch {
		case strings.HasPrefix(sql, "CREATE"):
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(sql, "INSERT"):
			mu.Lock()
			insertType = r.Header.Get("Content-Type")
			insertQuery = sql
			mu.Unlock()
			for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				var ev Event
				if err := json.Unmarshal([]byte(line), &ev); err != nil {
					t.Errorf("line: %v", err)
					http.Error(w, "bad", http.StatusBadRequest)
					return
				}
				mu.Lock()
				inserted = append(inserted, ev)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
		default:
			mu.Lock()
			queries = append(queries, sql)
			params = append(params, r.URL.RawQuery)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"data":[{"hits":"4"}],"rows":1}`)
		}
	}))
	defer srv.Close()

	ch, err := NewClickHouse(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	ev := sampleEvent()
	if err := ch.Push(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := ch.Push(context.Background(), []Event{ev}); err != nil {
		t.Fatal(err)
	}
	res, err := ch.Search(context.Background(), Query{Class: QueryFullText, Token: `check"out`})
	if err != nil || res.Hits != 4 {
		t.Fatalf("hits %d err %v", res.Hits, err)
	}
	res, err = ch.Search(context.Background(), Query{Class: QueryExact, Field: "status-code", Value: "200"})
	if err != nil || res.Hits != 4 {
		t.Fatalf("exact %d err %v", res.Hits, err)
	}
	res, err = ch.Search(context.Background(), filterQuery())
	if err != nil || res.Hits != 4 {
		t.Fatalf("filter %d err %v", res.Hits, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if user != "admin" || pass != "admin" || insertType != "application/x-ndjson" {
		t.Fatalf("auth %s/%s type %s", user, pass, insertType)
	}
	if !strings.Contains(insertQuery, "JSONEachRow") {
		t.Fatalf("insert %s", insertQuery)
	}
	if len(inserted) != 1 || inserted[0].Time != "2026-11-02 11:24:00.000" || inserted[0].Service != "checkout" {
		t.Fatalf("%+v", inserted)
	}
	if len(queries) != 3 || !strings.Contains(queries[0], "syscon.logs") || !strings.Contains(queries[0], "hasToken") || !strings.Contains(params[0], "check%22out") {
		t.Fatalf("fulltext %s %s", queries[0], params[0])
	}
	if !strings.Contains(queries[1], "`status-code`") || !strings.Contains(params[1], "param_val=200") {
		t.Fatalf("exact %s %s", queries[1], params[1])
	}
	if !strings.Contains(queries[2], "t_from:DateTime64") || !strings.Contains(params[2], "param_t_from=") {
		t.Fatalf("filter %s %s", queries[2], params[2])
	}
	if _, err := ch.Search(context.Background(), Query{Class: QueryExact, Field: "message", Value: "x"}); err == nil {
		t.Fatal("expected field error")
	}
}

func TestClickHouseStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := r.URL.Query().Get("query")
		if sql == "" {
			sql = string(body)
		}
		if strings.HasPrefix(sql, "CREATE") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ch, err := NewClickHouse(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = ch.Push(context.Background(), []Event{sampleEvent()})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("%v", err)
	}
	res, err := ch.Search(context.Background(), Query{Class: QueryFullText, Token: "checkout"})
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusServiceUnavailable {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(string(res.Body), "nope") {
		t.Fatalf("%q", res.Body)
	}
}

func TestClickHouseEnsureReadsExistingTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := r.URL.Query().Get("query")
		if sql == "" {
			sql = string(body)
		}
		switch {
		case strings.HasPrefix(sql, "CREATE"):
			http.Error(w, "denied", http.StatusForbidden)
		case strings.HasPrefix(sql, "SELECT 1 FROM syscon.logs"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(sql, "INSERT INTO syscon.logs"):
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	ch, err := NewClickHouse(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Push(context.Background(), []Event{sampleEvent()}); err != nil {
		t.Fatal(err)
	}
}

func TestNewClickHouseRejectsRelativeURL(t *testing.T) {
	if _, err := NewClickHouse("/logs", "", ""); err == nil {
		t.Fatal("expected error")
	}
}

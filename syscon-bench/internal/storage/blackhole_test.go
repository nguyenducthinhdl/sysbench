package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBlackholePush(t *testing.T) {
	var mu sync.Mutex
	var got []Event
	var user, pass, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		mu.Lock()
		user, pass, ctype = u, p, r.Header.Get("Content-Type")
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		var batch []Event
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Errorf("json: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		mu.Lock()
		got = append(got, batch...)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	sink, err := NewBlackhole(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	batch := []Event{
		{Message: "checkout info", StatusCode: 200, Service: "checkout", LogSeverity: "info"},
		{Message: "auth error\n\tat auth.api", StatusCode: 500, Service: "auth", LogSeverity: "error"},
	}
	if err := sink.Push(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if err := sink.Push(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if user != "admin" || pass != "admin" || ctype != "application/json" {
		t.Fatalf("auth %s/%s type %s", user, pass, ctype)
	}
	if len(got) != 2 || got[0].StatusCode != 200 || got[1].StatusCode != 500 || got[1].Message != "auth error\n\tat auth.api" {
		t.Fatalf("%+v", got)
	}
}

func TestBlackholeStatusAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	sink, err := NewBlackhole(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Push(context.Background(), []Event{{Message: "x", StatusCode: 200}})
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("err %v", err)
	}

	code := http.StatusInternalServerError
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
	defer srv2.Close()
	sink, err = NewBlackhole(srv2.URL, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Push(context.Background(), []Event{{StatusCode: 200}})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err %v", err)
	}

	h := Handler("admin", "secret")
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`[]`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete %d", rec.Code)
	}
}

func TestBlackholeSearch(t *testing.T) {
	var mu sync.Mutex
	var method, user, pass, class, token, field, value, from, to string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		mu.Lock()
		method, user, pass = r.Method, u, p
		class = r.URL.Query().Get("class")
		token = r.URL.Query().Get("q")
		field = r.URL.Query().Get("field")
		value = r.URL.Query().Get("value")
		from = r.URL.Query().Get("from")
		to = r.URL.Query().Get("to")
		mu.Unlock()
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("body: %v", err)
		}
		_, _ = io.WriteString(w, "ok\n")
	}))
	defer srv.Close()

	b, err := NewBlackhole(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	fromAt := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	toAt := fromAt.Add(time.Minute)
	res, err := b.Search(context.Background(), Query{
		Class: QueryFilter,
		Field: "service",
		Value: "checkout",
		From:  fromAt,
		To:    toAt,
		Token: "checkout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Hits != 0 || string(res.Body) != "ok\n" {
		t.Fatalf("%+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if method != http.MethodGet || user != "admin" || pass != "admin" {
		t.Fatalf("method %s auth %s/%s", method, user, pass)
	}
	if class != "filter" || token != "checkout" || field != "service" || value != "checkout" {
		t.Fatalf("query class=%s q=%s field=%s value=%s", class, token, field, value)
	}
	if from == "" || to == "" {
		t.Fatalf("window %s %s", from, to)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	b, err = NewBlackhole(bad.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err = b.Search(context.Background(), Query{Class: QueryFullText, Token: "checkout"})
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusServiceUnavailable {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(string(res.Body), "nope") {
		t.Fatalf("body %q", res.Body)
	}

	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), maxSearchBody+100))
	}))
	defer big.Close()
	b, err = NewBlackhole(big.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err = b.Search(context.Background(), Query{Class: QueryExact, Field: "uuid", Value: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Body) != maxSearchBody {
		t.Fatalf("kept %d", len(res.Body))
	}
}

func TestNewBlackholeRejectsRelativeURL(t *testing.T) {
	if _, err := NewBlackhole("/logs", "", ""); err == nil {
		t.Fatal("expected error")
	}
}

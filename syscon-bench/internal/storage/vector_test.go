package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVectorPushNDJSON(t *testing.T) {
	var got, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		ctype = r.Header.Get("Content-Type")
		if r.Header.Get("Authorization") != "" {
			t.Errorf("vector auth %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, err := NewVector(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Push(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	batch := []Event{
		{Message: "checkout info", Service: "checkout", StatusCode: 200, Time: "2026-11-02T11:24:00.000Z"},
		{Message: "auth error", Service: "auth", StatusCode: 500, LogSeverity: "error"},
	}
	if err := sink.Push(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if ctype != "application/x-ndjson" {
		t.Fatalf("content-type %s", ctype)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %d %q", len(lines), got)
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Service != "checkout" || ev.StatusCode != 200 || ev.Time != "2026-11-02T11:24:00.000Z" {
		t.Fatalf("%+v", ev)
	}
}

func TestVectorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "full", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	sink, err := NewVector(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Push(context.Background(), []Event{{Message: "x"}})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("%v", err)
	}
}

func TestNewVectorRejectsRelativeURL(t *testing.T) {
	if _, err := NewVector("/logs"); err == nil {
		t.Fatal("expected error")
	}
}

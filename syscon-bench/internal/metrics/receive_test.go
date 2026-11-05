package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReceiver(t *testing.T) {
	r := &Receiver{Username: "admin", Password: "admin"}
	h := r.Handler()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bench_ingest_events_total 1\n"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}

	req.SetBasicAuth("admin", "admin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("post %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != ContentType {
		t.Fatalf("type %s", rec.Header().Get("Content-Type"))
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "bench_ingest_events_total 1\n" {
		t.Fatalf("%q", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/metrics", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete %d", rec.Code)
	}
}

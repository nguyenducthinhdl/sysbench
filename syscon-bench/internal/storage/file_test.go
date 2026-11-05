package storage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFileLogAppendsJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenFileLog(path)
	if err != nil {
		t.Fatal(err)
	}
	first := []Event{{
		Message:     "auth error\n\tat auth.api",
		StatusCode:  500,
		LogSeverity: "error",
		Service:     "auth",
		Version:     "1.0.0",
	}}
	if err := log.Append(first); err != nil {
		t.Fatal(err)
	}
	if err := log.Append([]Event{{Message: "checkout info", StatusCode: 200, Service: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(nil); err != nil {
		t.Fatal(err)
	}
	if log.Count() != 2 {
		t.Fatalf("count %d", log.Count())
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines %d", len(lines))
	}
	if strings.Contains(lines[0], "\n") {
		t.Fatal("event leaked a raw newline")
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.StatusCode != 500 || ev.Message != "auth error\n\tat auth.api" || ev.Service != "auth" {
		t.Fatalf("%+v", ev)
	}
	if !strings.Contains(lines[0], `"status-code":500`) {
		t.Fatalf("status not an int: %s", lines[0])
	}
}

func TestFileLogHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenFileLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	h := log.Handler("admin", "secret")

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`[{"message":"m","status-code":200}]`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`[{"message":"m","status-code":200,"service":"checkout"}]`)))
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"message":"one"}`)))
	req.SetBasicAuth("admin", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "events 1" {
		t.Fatalf("get %d %q", rec.Code, rec.Body.String())
	}
}

func TestFileLogConcurrentAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenFileLog(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev := Event{Message: "m", StatusCode: 200, Service: "checkout", UUID: string(rune('a' + i))}
			for n := 0; n < 20; n++ {
				if err := log.Append([]Event{ev}); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	if len(lines) != 160 {
		t.Fatalf("lines %d", len(lines))
	}
	for _, line := range lines {
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("torn line %q: %v", line, err)
		}
	}
}

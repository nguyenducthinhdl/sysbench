package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"syscon-bench/internal/generate"
	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

func TestRunUsage(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("expected usage error")
	}
	if err := run([]string{"run"}); err == nil || !strings.Contains(err.Error(), "--scenario") {
		t.Fatalf("%v", err)
	}
	if err := run([]string{"run", "--scenario", "x", "--storage-name", "blackhole"}); err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("%v", err)
	}
	if err := run([]string{"nope"}); err == nil {
		t.Fatal("expected unknown command")
	}
	if err := run([]string{"blackhole", "--username", "admin"}); err == nil {
		t.Fatal("expected auth pair error")
	}
	if err := run([]string{"file", "--username", "admin"}); err == nil {
		t.Fatal("expected file auth pair error")
	}
}

func TestRunWiresEngineStorage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.yaml")
	doc := strings.ReplaceAll(shortYAML, "STORAGE", "http://127.0.0.1:9")
	doc = strings.ReplaceAll(doc, "DASH", "http://127.0.0.1:9")
	doc = strings.Replace(doc, "name: blackhole", "name: clickhouse", 1)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"run", "--scenario", path})
	if err == nil || strings.Contains(err.Error(), "not implemented") || !strings.Contains(err.Error(), "fields") {
		t.Fatalf("%v", err)
	}
	for _, name := range []string{"clickhouse", "elasticsearch", "loki", "victorialogs"} {
		sink, err := openSink(scenario.Target{
			Name:     name,
			Endpoint: "http://127.0.0.1:9",
			Protocol: "http",
			Auth:     scenario.Auth{Type: "basic", Username: "admin", Password: "admin"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sink.(storage.Querier); !ok {
			t.Fatalf("%s is not a querier", name)
		}
	}
	if _, err := openSink(scenario.Target{Name: "nope", Endpoint: "http://127.0.0.1:9"}); err == nil {
		t.Fatal("expected unknown storage")
	}
	if err := run([]string{"dashboard", "--username", "admin"}); err == nil {
		t.Fatal("expected dashboard auth pair error")
	}
}

func TestRunAgainstBlackhole(t *testing.T) {
	var mu sync.Mutex
	var events []storage.Event
	storageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "admin" || p != "admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var batch []storage.Event
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Errorf("json: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		mu.Lock()
		events = append(events, batch...)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer storageSrv.CloseClientConnections()
	defer storageSrv.Close()

	var dashBody string
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "admin" || p != "admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
			t.Errorf("content-type %q", r.Header.Get("Content-Type"))
		}
		b, _ := io.ReadAll(r.Body)
		dashBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer dash.CloseClientConnections()
	defer dash.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "s.yaml")
	doc := strings.ReplaceAll(shortYAML, "STORAGE", "http://127.0.0.1:9")
	doc = strings.ReplaceAll(doc, "DASH", dash.URL)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	fields, messages := writeCatalog(t, dir)
	err := run([]string{
		"run", "--scenario", path,
		"--fields", fields, "--messages", messages,
		"--storage-name", "blackhole", "--storage-endpoint", storageSrv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 20 {
		t.Fatalf("events %d", len(events))
	}
	var messageBytes int
	seenService := map[string]struct{}{}
	for _, ev := range events {
		if ev.StatusCode != 200 || ev.Version != "1.0.0" {
			t.Fatalf("%+v", ev)
		}
		if !strings.HasPrefix(ev.InstanceID, "instance-") || !strings.HasPrefix(ev.HostName, "host-") || !strings.HasPrefix(ev.IPAddress, "10.") {
			t.Fatalf("identity %+v", ev)
		}
		if !strings.Contains(ev.Message, "usr-") || strings.Contains(ev.Message, "#user-id") {
			t.Fatalf("message %q", ev.Message)
		}
		seenService[ev.Service] = struct{}{}
		messageBytes += len(ev.Message)
	}
	if len(seenService) == 0 {
		t.Fatal("no service")
	}
	if !strings.Contains(dashBody, "bench_ingest_events_total") || !strings.Contains(dashBody, "20") {
		t.Fatalf("dashboard %s", dashBody)
	}
	if !strings.Contains(dashBody, "bench_ingest_bytes_total") || !strings.Contains(dashBody, strconv.Itoa(messageBytes)) {
		t.Fatalf("bytes %d\n%s", messageBytes, dashBody)
	}
}

func writeCatalog(t *testing.T, dir string) (fields, messages string) {
	t.Helper()
	fields = filepath.Join(dir, "fields.csv")
	if err := generate.WriteFields(fields); err != nil {
		t.Fatal(err)
	}
	messages = filepath.Join(dir, "messages.txt")
	body := "accepted GET /users/#user-id request #track-id\n"
	if err := os.WriteFile(messages, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return fields, messages
}

func TestServeBlackhole(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- serveBlackhole(ctx, ln, "admin", "admin")
	}()
	url := "http://" + ln.Addr().String()
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader("[]"))
		req.SetBasicAuth("admin", "admin")
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	store, err := storage.OpenFileLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- serve(ctx, ln, store.Handler("admin", "admin"))
	}()
	body := `[{"message":"checkout info","status-code":200,"service":"checkout"}]`
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String(), strings.NewReader(body))
		req.SetBasicAuth("admin", "admin")
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev storage.Event
	if err := json.Unmarshal(bytesTrim(raw), &ev); err != nil {
		t.Fatalf("file %q: %v", raw, err)
	}
	if ev.StatusCode != 200 || ev.Service != "checkout" || ev.Message != "checkout info" {
		t.Fatalf("%+v", ev)
	}
}

func TestRunViaVector(t *testing.T) {
	var mu sync.Mutex
	var body string
	vector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body += string(b)
		mu.Unlock()
		if r.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Errorf("content-type %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer vector.Close()
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("engine received %s", r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer engine.Close()
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer dash.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "s.yaml")
	doc := strings.ReplaceAll(shortYAML, "STORAGE", "http://127.0.0.1:9")
	doc = strings.ReplaceAll(doc, "DASH", dash.URL)
	doc = strings.Replace(doc, "name: blackhole", "name: clickhouse", 1)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	fields, messages := writeCatalog(t, dir)
	err := run([]string{
		"run", "--scenario", path,
		"--fields", fields, "--messages", messages,
		"--storage-name", "clickhouse", "--storage-endpoint", engine.URL,
		"--via", "vector", "--vector-endpoint", vector.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, "\n") || !strings.Contains(body, `"message"`) {
		t.Fatalf("vector body %q", body)
	}
	if strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Fatalf("expected ndjson, got %q", body)
	}
}

func TestWriteCommand(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "preload.jsonl")
	err := run([]string{"write", "--out", out, "--bytes", "1500", "--min-size", "64", "--max-size", "64", "--error-rate", "0"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 1500 {
		t.Fatalf("size %d", info.Size())
	}
	if err := run([]string{"write"}); err == nil || !strings.Contains(err.Error(), "--out") {
		t.Fatalf("%v", err)
	}
	if err := run([]string{"run", "--scenario", "x", "--via", "nope"}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("%v", err)
	}
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

const shortYAML = `name: short-run
storage:
  - name: blackhole
    endpoint: STORAGE
    protocol: http
    auth:
      type: basic
      username: admin
      password: admin
dashboard:
  - name: dashboard-01
    endpoint: DASH
    protocol: http
    auth:
      type: basic
      username: admin
      password: admin
configs:
  min_size: 64
  max_size: 64
  concurrent: 2
  duration: 1
scenario:
  - name: linear-all
    time_stamp: 1
    type: linear
    rps_min_concurrent: 20
    rps_max_concurrent: 20
    error_rate: 0
    query_rate: 0
`

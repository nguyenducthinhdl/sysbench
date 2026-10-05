package logx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLineFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.log")
	lg, err := Open(path, "client-1", "10.0.0.8")
	if err != nil {
		t.Fatal(err)
	}
	lg.Info("http", "POST /payments -> 200", `{"order_id":"ord-1"}`)
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z client-1/10\.0\.0\.8 INFO http POST /payments -> 200 \(\{"order_id":"ord-1"\}\)\n$`)
	if !re.Match(got) {
		t.Fatalf("record:\n%s", got)
	}
}

func TestStackBodySpansLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.log")
	lg, err := Open(path, "payments", "10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	lg.Error("payments", "request failed /payments", "goroutine 1 [running]:\nmain.fail()")
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "ERROR payments request failed /payments (goroutine 1 [running]:\nmain.fail())\n") {
		t.Fatalf("record:\n%s", text)
	}
	if strings.Count(text, "\n") != 2 {
		t.Fatalf("want a two-line body, got:\n%s", text)
	}
}

func TestPanicWritesStackThenPanics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panic.log")
	lg, err := Open(path, "booking", "10.0.0.10")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatal("expected panic")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(got)
		if !strings.Contains(text, "ERROR runtime scheduled panic (") || !strings.Contains(text, "goroutine") {
			t.Fatalf("panic record:\n%s", text)
		}
		if !strings.HasSuffix(text, ")\n") {
			t.Fatalf("record does not close the body:\n%s", text)
		}
	}()
	lg.Panic("runtime", "scheduled panic")
}

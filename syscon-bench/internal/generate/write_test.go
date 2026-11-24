package generate

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"syscon-bench/internal/storage"
)

func TestWriteFile(t *testing.T) {
	path := t.TempDir() + "/preload.jsonl"
	res, err := WriteFile(path, 3000, 64, 64, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Events < 1 || res.Bytes < 3000 {
		t.Fatalf("%+v", res)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != res.Bytes {
		t.Fatalf("stat %d result %d", info.Size(), res.Bytes)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var n int
	for sc.Scan() {
		var ev storage.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatal(err)
		}
		if len(ev.Message) != 64 || ev.Version != "1.0.0" || ev.StatusCode != 200 || ev.LogSeverity != "info" {
			t.Fatalf("%+v", ev)
		}
		if !strings.HasPrefix(ev.InstanceID, "instance-") || !strings.HasPrefix(ev.HostName, "host-") || !strings.HasPrefix(ev.IPAddress, "10.") {
			t.Fatalf("identity %+v", ev)
		}
		if ev.Time == "" || ev.UUID == "" || len(ev.TraceID) != 32 {
			t.Fatalf("ids %+v", ev)
		}
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if int64(n) != res.Events {
		t.Fatalf("lines %d events %d", n, res.Events)
	}
}

func TestWriteRowsUsesCatalogAndRepeats(t *testing.T) {
	cat := &Catalog{
		InstanceIDs: []string{"instance-0007"},
		IPs:         []string{"10.1.2.3"},
		Hosts:       []string{"host-0007"},
		Services:    []string{"payments-0007"},
		Messages:    []string{"paid #user-id"},
	}
	dir := t.TempDir()
	first := dir + "/a.jsonl"
	second := dir + "/b.jsonl"
	res, err := WriteRows(first, 4, cat, 0, 0, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if res.Events != 4 || res.Bytes < 4 {
		t.Fatalf("%+v", res)
	}
	if _, err := WriteRows(second, 4, cat, 0, 0, 0, 7); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("same seed rewrote a different file")
	}
	sc := bufio.NewScanner(strings.NewReader(string(a)))
	n := 0
	for sc.Scan() {
		var ev storage.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.InstanceID != "instance-0007" || ev.IPAddress != "10.1.2.3" || ev.Service != "payments-0007" {
			t.Fatalf("%+v", ev)
		}
		if !strings.Contains(ev.Message, "usr-") || strings.Contains(ev.Message, "#user-id") {
			t.Fatalf("message %q", ev.Message)
		}
		n++
	}
	if n != 4 {
		t.Fatalf("lines %d", n)
	}
}

func TestWriteFileRejectsSize(t *testing.T) {
	if _, err := WriteFile(t.TempDir()+"/x", 10, 10, 10, 0, 1); err == nil {
		t.Fatal("expected size error")
	}
}

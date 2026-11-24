package generate

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syscon-bench/internal/scenario"
)

func TestFieldsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fields.csv")
	if err := WriteFields(path); err != nil {
		t.Fatal(err)
	}
	cat, err := loadFields(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Services) != PoolSize || cat.Services[0] == cat.Services[1] {
		t.Fatalf("services %d %q %q", len(cat.Services), cat.Services[0], cat.Services[1])
	}
	if cat.Services[0] == "users-0000" && cat.Services[1] == "payments-0001" {
		t.Fatal("services are still sequential")
	}
	if cat.InstanceIDs[0] != "instance-0000" || cat.IPs[999] != "10.3.231.10" || cat.Hosts[7] != "host-0007" {
		t.Fatalf("ends %+v", cat)
	}
}

func TestMaskAndRender(t *testing.T) {
	line := maskLine(`GET /users/usr-96748 /order/ord-1 /payments/pay-2 /bookings/bk-3 request_id=17ce94b3-6bb0-4053-beed-d3d62f7be5e4`)
	for _, token := range []string{"#user-id", "#order-id", "#payment-id", "#booking-id", "#track-id"} {
		if !strings.Contains(line, token) {
			t.Fatalf("missing %s in %s", token, line)
		}
	}
	if strings.Contains(line, "usr-") || strings.Contains(line, "17ce94b3") {
		t.Fatalf("raw id survived: %s", line)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	got := renderMessage(rng, line)
	if strings.Contains(got, "#") {
		t.Fatalf("placeholder left in %s", got)
	}
	if !strings.Contains(got, "usr-") || !strings.Contains(got, "ord-") {
		t.Fatalf("fill %s", got)
	}
}

func TestPrepareSmallLogs(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.Mkdir(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "GET /users/usr-10 id 11111111-2222-3333-4444-555555555555\n\nsecond usr-2\n"
	if err := os.WriteFile(filepath.Join(logs, "users.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "note.txt"), []byte("skip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fields := filepath.Join(dir, "fields.csv")
	messages := filepath.Join(dir, "messages.txt")
	n, err := Prepare(logs, fields, messages, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("lines %d", n)
	}
	cat, err := LoadCatalog(fields, messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Messages) != 1 || !strings.Contains(cat.Messages[0], "#user-id") || strings.Contains(cat.Messages[0], "usr-") {
		t.Fatalf("message %q", cat.Messages[0])
	}
}

func TestCatalogEventDrawsFromFiles(t *testing.T) {
	cat := &Catalog{
		InstanceIDs: []string{"instance-0001"},
		IPs:         []string{"10.1.2.10"},
		Hosts:       []string{"host-0008"},
		Services:    []string{"payments-0004"},
		Messages:    []string{"paid #user-id track #track-id"},
	}
	rng := rand.New(rand.NewPCG(4, 5))
	ev := newEvent(rng, cat, 64, 64, scenario.Segment{ErrorRate: 0}, 1, time.Unix(0, 0).UTC())
	if ev.InstanceID != "instance-0001" || ev.IPAddress != "10.1.2.10" || ev.HostName != "host-0008" || ev.Service != "payments-0004" {
		t.Fatalf("%+v", ev)
	}
	if strings.Contains(ev.Message, "#") || !strings.Contains(ev.Message, "usr-") {
		t.Fatalf("message %q", ev.Message)
	}
	if len(ev.Message) == 64 {
		t.Fatal("catalog message was padded to min_size")
	}
}

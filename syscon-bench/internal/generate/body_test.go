package generate

import (
	"encoding/json"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

func TestIdentities(t *testing.T) {
	if len(identities) != PoolSize {
		t.Fatalf("pool %d", len(identities))
	}
	cases := []struct {
		i            int
		id, host, ip string
	}{
		{0, "instance-0000", "host-0000", "10.0.0.10"},
		{7, "instance-0007", "host-0007", "10.0.7.10"},
		{255, "instance-0255", "host-0255", "10.0.255.10"},
		{256, "instance-0256", "host-0256", "10.1.0.10"},
		{999, "instance-0999", "host-0999", "10.3.231.10"},
	}
	for _, tc := range cases {
		got := identityAt(tc.i)
		if got.instanceID != tc.id || got.host != tc.host || got.ip != tc.ip {
			t.Fatalf("i %d got %+v", tc.i, got)
		}
	}
}

func TestIllustrativeBodies(t *testing.T) {
	now := time.Date(2026, time.November, 2, 11, 24, 0, 0, time.UTC)
	cases := []struct {
		format string
		status int
		sev    string
		want   string
	}{
		{formatJSON, 200, "info", `{"sev":"info","service":"checkout","component":"api","msg":"request completed"}`},
		{formatAccess, 200, "info", `10.0.0.10 - checkout info [02/Nov/2026:11:24:00 +0000] "GET /v1/checkout HTTP/1.1" 200 128 "-" "syscon-bench"`},
		{formatText, 200, "info", "checkout info request completed component api"},
		{formatStacktrace, 200, "info", "checkout info exception timeout\n\tat checkout.api.Worker.run(Worker.java:42)"},
		{formatAudit, 200, "info", "audit checkout info service=checkout actor=instance-0000 action=access result=200"},
		{formatLogfmt, 200, "info", `service=checkout component=api severity=info status=200 msg="request completed"`},
		{formatAccess, 500, "error", `10.0.0.10 - checkout error [02/Nov/2026:11:24:00 +0000] "GET /v1/checkout HTTP/1.1" 500 128 "-" "syscon-bench"`},
		{formatLogfmt, 500, "error", `service=checkout component=api severity=error status=500 msg="request completed"`},
	}
	for _, tc := range cases {
		got := messageCore(tc.format, "checkout", "api", tc.sev, tc.status, "10.0.0.10", "instance-0000", now)
		if got != tc.want {
			t.Fatalf("%s:\n got %q\nwant %q", tc.format, got, tc.want)
		}
	}
}

func TestMessageTokensSurviveCutAndPad(t *testing.T) {
	now := time.Date(2026, time.November, 2, 18, 24, 0, 0, time.FixedZone("ICT", 7*3600))
	formats := []string{formatJSON, formatAccess, formatText, formatStacktrace, formatAudit, formatLogfmt}
	var pairs []pair
	pairs = append(pairs, applicationPairs...)
	pairs = append(pairs, webPairs...)
	pairs = append(pairs, platformPairs...)
	pairs = append(pairs, dataPairs...)
	for _, format := range formats {
		for _, p := range pairs {
			for _, sev := range []struct {
				name   string
				status int
			}{{"info", 200}, {"error", 500}} {
				core := messageCore(format, p.service, p.component, sev.name, sev.status, "10.3.231.10", "instance-0999", now)
				got := fitMessage(core, 64)
				if len(got) != 64 {
					t.Fatalf("len %d", len(got))
				}
				if !hasToken(got, p.service) || !hasToken(got, sev.name) {
					t.Fatalf("%s %s %s missing token in %q", format, p.service, sev.name, got)
				}
				if format == formatStacktrace && !strings.Contains(core[:min(64, len(core))], "\n") {
					t.Fatalf("stacktrace newline not in prefix %q", core)
				}
				wide := fitMessage(core, 400)
				if len(wide) != 400 {
					t.Fatalf("wide %d", len(wide))
				}
				if len(core) <= 400 && !strings.HasPrefix(wide, core) {
					t.Fatalf("prefix lost %s %s", format, p.service)
				}
				if len(core) <= 400 {
					rest := wide[len(core):]
					for i := 0; i < len(rest); i++ {
						want := byte(' ')
						if i%2 == 1 {
							want = 'x'
						}
						if rest[i] != want {
							t.Fatalf("pad %d = %q", i, rest[i])
						}
					}
				}
			}
		}
	}
}

func TestNewEventSchema(t *testing.T) {
	now := time.Date(2026, time.November, 2, 18, 24, 0, 123000000, time.FixedZone("ICT", 7*3600))
	rng := rand.New(rand.NewPCG(1, 2))
	seg := scenario.Segment{ErrorRate: 0}
	ev := newEvent(rng, nil, 80, 120, seg, 1, now)
	if ev.Time != "2026-11-02T11:24:00.123Z" {
		t.Fatalf("time %s", ev.Time)
	}
	if ev.Version != "1.0.0" || ev.LogSeverity != "info" || ev.StatusCode != 200 {
		t.Fatalf("%+v", ev)
	}
	if len(ev.Message) < 80 || len(ev.Message) > 120 {
		t.Fatalf("message len %d", len(ev.Message))
	}
	if !uuidRE.MatchString(ev.UUID) || !traceRE.MatchString(ev.TraceID) || !timeRE.MatchString(ev.Time) {
		t.Fatalf("ids %s %s %s", ev.UUID, ev.TraceID, ev.Time)
	}
	if !allowedPair(ev.Service, ev.Component) {
		t.Fatalf("pair %s/%s", ev.Service, ev.Component)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"message"`, `"uuid"`, `"instance-id"`, `"ip-address"`, `"host-name"`, `"log-severity"`, `"status-code"`, `"component"`, `"trace-id"`, `"version"`, `"service"`, `"time"`, `"jira-id"`, `"group-id"`, `"external-01"`, `"external-02"`, `"external-03"`, `"binary-field"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
	if !strings.Contains(string(raw), `"status-code":200`) {
		t.Fatalf("status-code not an int: %s", raw)
	}

	seg.ErrorRate = 1
	bad := newEvent(rng, nil, 64, 64, seg, 2, now)
	if bad.LogSeverity != "error" || bad.StatusCode != 500 {
		t.Fatalf("error event %+v", bad)
	}
	raw, _ = json.Marshal(bad)
	if !strings.Contains(string(raw), `"status-code":500`) {
		t.Fatalf("error status: %s", raw)
	}
}

func TestMixAndUniqueness(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	seg := scenario.Segment{ErrorRate: 0.25}
	const n = 20000
	formats := map[string]int{}
	sources := map[string]int{}
	errors := 0
	uuids := map[string]struct{}{}
	traces := map[string]struct{}{}
	for i := 0; i < n; i++ {
		ev := newEvent(rng, nil, 64, 180, seg, uint64(i+1), time.Unix(0, 0).UTC())
		formats[detectFormat(ev.Message)]++
		sources[sourceClass(ev.Service)]++
		if ev.StatusCode != 200 && ev.StatusCode != 500 {
			t.Fatalf("status %d", ev.StatusCode)
		}
		if (ev.LogSeverity == "error") != (ev.StatusCode == 500) {
			t.Fatalf("severity %s status %d", ev.LogSeverity, ev.StatusCode)
		}
		if ev.LogSeverity == "error" {
			errors++
		}
		if _, ok := uuids[ev.UUID]; ok {
			t.Fatalf("duplicate uuid %s", ev.UUID)
		}
		uuids[ev.UUID] = struct{}{}
		if _, ok := traces[ev.TraceID]; ok {
			t.Fatalf("duplicate trace %s", ev.TraceID)
		}
		traces[ev.TraceID] = struct{}{}
		head := ev.Message
		if len(head) > 64 {
			head = head[:64]
		}
		if !hasToken(head, ev.Service) || !hasToken(head, ev.LogSeverity) {
			t.Fatalf("tokens missing in %q", head)
		}
		if ev.JiraID == "" || ev.GroupID == "" || ev.External01 == "" || len(ev.BinaryField) == 0 {
			t.Fatal("empty extra field")
		}
	}
	assertShare(t, formats["json"], n, 0.60, 0.03)
	assertShare(t, formats["access"], n, 0.10, 0.02)
	assertShare(t, formats["text"], n, 0.10, 0.02)
	assertShare(t, formats["stacktrace"], n, 0.05, 0.015)
	assertShare(t, formats["audit"], n, 0.05, 0.015)
	assertShare(t, formats["logfmt"], n, 0.10, 0.02)
	assertShare(t, sources["application"], n, 0.60, 0.03)
	assertShare(t, sources["web"], n, 0.20, 0.02)
	assertShare(t, sources["platform"], n, 0.10, 0.02)
	assertShare(t, sources["data"], n, 0.10, 0.02)
	assertShare(t, errors, n, 0.25, 0.03)

	none := scenario.Segment{ErrorRate: 0}
	all := scenario.Segment{ErrorRate: 1}
	for i := 0; i < 100; i++ {
		if newEvent(rng, nil, 64, 64, none, uint64(i+1), time.Now()).StatusCode != 200 {
			t.Fatal("error_rate 0 produced an error")
		}
		if newEvent(rng, nil, 64, 64, all, uint64(i+1), time.Now()).StatusCode != 500 {
			t.Fatal("error_rate 1 produced a success")
		}
	}
}

func assertShare(t *testing.T, got, n int, want, tol float64) {
	t.Helper()
	share := float64(got) / float64(n)
	if share < want-tol || share > want+tol {
		t.Fatalf("share %v want %v ± %v (%d/%d)", share, want, tol, got, n)
	}
}

func detectFormat(msg string) string {
	switch {
	case strings.HasPrefix(msg, "{"):
		return "json"
	case strings.HasPrefix(msg, "audit "):
		return "audit"
	case strings.HasPrefix(msg, "service="):
		return "logfmt"
	case strings.Contains(msg, "\n"):
		return "stacktrace"
	case len(msg) > 0 && msg[0] >= '0' && msg[0] <= '9':
		return "access"
	default:
		return "text"
	}
}

func sourceClass(service string) string {
	switch service {
	case "checkout", "payments", "inventory", "auth", "notifications":
		return "application"
	case "edge", "gateway", "ingress":
		return "web"
	case "kubelet", "containerd", "kernel":
		return "platform"
	case "postgres", "redis", "kafka":
		return "data"
	default:
		return "other"
	}
}

func allowedPair(service, component string) bool {
	for _, p := range append(append(append(append([]pair{}, applicationPairs...), webPairs...), platformPairs...), dataPairs...) {
		if p.service == service && p.component == component {
			return true
		}
	}
	return false
}

func hasToken(s, tok string) bool {
	for _, got := range tokens(s) {
		if got == tok {
			return true
		}
	}
	return false
}

func tokens(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		if isAlnum(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func isAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

var (
	uuidRE  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	traceRE = regexp.MustCompile(`^[0-9a-f]{32}$`)
	timeRE  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$`)
)

func TestJSONRoundTripKeepsStatusInt(t *testing.T) {
	ev := storage.Event{StatusCode: 500, Message: "auth error", Version: "1.0.0"}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var back storage.Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.StatusCode != 500 {
		t.Fatalf("status %d", back.StatusCode)
	}
}

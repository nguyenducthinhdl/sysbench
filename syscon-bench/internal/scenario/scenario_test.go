package scenario

import (
	"strings"
	"testing"
)

func TestLoadCheckedIn(t *testing.T) {
	sc, err := Load("../../log-generator/scenarios/test-scenario-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Name != "test-scenario-01" {
		t.Fatalf("name %q", sc.Name)
	}
	if sc.Storage.Name != "blackhole" || sc.Storage.Protocol != "http" || sc.Storage.Auth.Type != "basic" {
		t.Fatalf("storage %+v", sc.Storage)
	}
	if sc.Storage.Auth.Username != "admin" || sc.Storage.Auth.Password != "admin" {
		t.Fatalf("auth %+v", sc.Storage.Auth)
	}
	if sc.Dashboard.Name != "dashboard-01" || sc.Dashboard.Endpoint != "http://localhost:8001" {
		t.Fatalf("dashboard %+v", sc.Dashboard)
	}
	if sc.Configs.MinSize != 100 || sc.Configs.MaxSize != 200000 || sc.Configs.Concurrent != 100 || sc.Configs.Duration != 3600 {
		t.Fatalf("configs %+v", sc.Configs)
	}
	if len(sc.Segments) != 1 {
		t.Fatalf("segments %d", len(sc.Segments))
	}
	seg := sc.Segments[0]
	if seg.Name != "linear-all" || seg.Start != 0 || seg.TimeStamp != 3600 || seg.Type != "linear" {
		t.Fatalf("segment %+v", seg)
	}
	if seg.RPSMin != 100 || seg.RPSMax != 100 || seg.ErrorRate != 0.01 || seg.QueryRate != 1 {
		t.Fatalf("rates %+v", seg)
	}

	sc2, err := Load("../../log-generator/scenarios/test-scenario-02.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name       string
		start, end float64
		kind       string
	}{
		{"linear-01", 0, 60, "linear"},
		{"spike-01", 60, 240, "spike"},
		{"spike-02", 240, 1800, "spike"},
		{"linear-02", 1800, 3000, "linear"},
		{"spike-03", 3000, 3600, "spike"},
	}
	if len(sc2.Segments) != len(want) {
		t.Fatalf("segments %d", len(sc2.Segments))
	}
	for i, w := range want {
		seg := sc2.Segments[i]
		if seg.Name != w.name || seg.Start != w.start || seg.TimeStamp != w.end || seg.Type != w.kind {
			t.Fatalf("segment %d %+v", i, seg)
		}
	}
	if sc2.Segments[4].RPSMin != 2000 || sc2.Segments[4].RPSMax != 100 {
		t.Fatalf("spike-03 %+v", sc2.Segments[4])
	}
}

func TestParseFractionalAndWhole(t *testing.T) {
	doc := strings.Replace(baseYAML, "duration: 10", "duration: 10.5", 1)
	doc = strings.Replace(doc, "time_stamp: 10", "time_stamp: 10.5", 1)
	doc = strings.Replace(doc, "concurrent: 1", "concurrent: 1.0", 1)
	doc = strings.Replace(doc, "min_size: 64", "min_size: 64.0", 1)
	doc = strings.Replace(doc, "rps_min_concurrent: 1", "rps_min_concurrent: 0.5", 1)
	sc := parseOK(t, doc)
	if sc.Configs.Duration != 10.5 || sc.Segments[0].TimeStamp != 10.5 {
		t.Fatalf("duration %+v seg %+v", sc.Configs, sc.Segments[0])
	}
	if sc.Configs.Concurrent != 1 || sc.Configs.MinSize != 64 || sc.Segments[0].RPSMin != 0.5 {
		t.Fatalf("parsed %+v %+v", sc.Configs, sc.Segments[0])
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"empty", "", "empty"},
		{"bad name", strings.Replace(baseYAML, "name: demo", "name: Demo", 1), "name"},
		{"storage name", strings.Replace(baseYAML, "name: blackhole", "name: redis", 1), "blackhole"},
		{"relative endpoint", strings.Replace(baseYAML, "endpoint: http://127.0.0.1:8000", "endpoint: /logs", 1), "absolute"},
		{"protocol mismatch", strings.Replace(baseYAML, "endpoint: http://127.0.0.1:8000\n    protocol: http", "endpoint: http://127.0.0.1:8000\n    protocol: https", 1), "protocol"},
		{"auth type", strings.Replace(baseYAML, "type: none", "type: token", 1), "auth type"},
		{"none with user", strings.Replace(baseYAML, "    auth:\n      type: none\n", "    auth:\n      type: none\n      username: admin\n", 1), "omit username"},
		{"basic missing password", strings.Replace(baseYAML, "    auth:\n      type: none\n", "    auth:\n      type: basic\n      username: admin\n", 1), "password"},
		{"min size", strings.Replace(baseYAML, "min_size: 64", "min_size: 63", 1), "min_size"},
		{"max below min", strings.Replace(strings.Replace(baseYAML, "min_size: 64", "min_size: 200", 1), "max_size: 128", "max_size: 100", 1), "max_size"},
		{"max too big", strings.Replace(baseYAML, "max_size: 128", "max_size: 1048577", 1), "max_size"},
		{"concurrent fraction", strings.Replace(baseYAML, "concurrent: 1", "concurrent: 1.5", 1), "integer"},
		{"concurrent zero", strings.Replace(baseYAML, "concurrent: 1", "concurrent: 0", 1), "concurrent"},
		{"duration zero", strings.Replace(baseYAML, "duration: 10", "duration: 0", 1), "duration"},
		{"no segments", strings.Replace(baseYAML, "scenario:\n  - name: linear-all\n    time_stamp: 10\n    type: linear\n    rps_min_concurrent: 1\n    rps_max_concurrent: 2\n    error_rate: 0\n    query_rate: 0\n", "scenario: []\n", 1), "at least one"},
		{"timestamp not duration", strings.Replace(baseYAML, "time_stamp: 10", "time_stamp: 9", 1), "duration"},
		{"zero timestamp", strings.Replace(baseYAML, "time_stamp: 10", "time_stamp: 0", 1), "time_stamp"},
		{"bad type", strings.Replace(baseYAML, "type: linear", "type: step", 1), "linear or spike"},
		{"negative rps", strings.Replace(baseYAML, "rps_min_concurrent: 1", "rps_min_concurrent: -1", 1), "rps_min_concurrent"},
		{"error rate", strings.Replace(baseYAML, "error_rate: 0", "error_rate: 1.1", 1), "error_rate"},
		{"query rate", strings.Replace(baseYAML, "query_rate: 0", "query_rate: -1", 1), "query_rate"},
		{"unknown field", baseYAML + "foo: 1\n", "foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parseErr(t, tc.doc, tc.want)
		})
	}
}

func TestDuplicateSegmentAndTwoTargets(t *testing.T) {
	doc := strings.Replace(baseYAML, "duration: 10", "duration: 20", 1)
	doc = strings.Replace(doc, "scenario:\n", `scenario:
  - name: linear-all
    time_stamp: 10
    type: linear
    rps_min_concurrent: 1
    rps_max_concurrent: 1
    error_rate: 0
    query_rate: 0
`, 1)
	parseErr(t, doc, "duplicate")

	two := strings.Replace(baseYAML, "storage:\n", `storage:
  - name: loki
    endpoint: http://127.0.0.1:3100
    protocol: http
    auth:
      type: none
`, 1)
	parseErr(t, two, "exactly one")
	parseErr(t, baseYAML+"---\nname: other\n", "multiple")
}

func TestAuthNoneAndBasic(t *testing.T) {
	sc := parseOK(t, baseYAML)
	if sc.Storage.Auth.Type != "none" || sc.Storage.Auth.Username != "" {
		t.Fatalf("%+v", sc.Storage.Auth)
	}
	doc := strings.Replace(baseYAML, "    auth:\n      type: none\n", "    auth:\n      type: basic\n      username: admin\n      password: secret\n", 2)
	sc = parseOK(t, doc)
	if sc.Storage.Auth.Password != "secret" || sc.Dashboard.Auth.Username != "admin" {
		t.Fatalf("storage %+v dashboard %+v", sc.Storage.Auth, sc.Dashboard.Auth)
	}
}

func TestOverrideStorage(t *testing.T) {
	sc := parseOK(t, baseYAML)
	sc.Storage.Auth = Auth{Type: "basic", Username: "admin", Password: "secret"}
	if err := sc.OverrideStorage("elasticsearch", "https://example.com:9200/bulk"); err != nil {
		t.Fatal(err)
	}
	if sc.Storage.Name != "elasticsearch" || sc.Storage.Protocol != "https" {
		t.Fatalf("%+v", sc.Storage)
	}
	if sc.Storage.Endpoint != "https://example.com:9200/bulk" || sc.Storage.Auth.Password != "secret" {
		t.Fatalf("%+v", sc.Storage)
	}
	if err := sc.OverrideStorage("nope", "http://127.0.0.1:9"); err == nil {
		t.Fatal("expected bad name")
	}
	if err := sc.OverrideStorage("clickhouse", "ftp://example.com"); err == nil {
		t.Fatal("expected bad url")
	}
}

func parseOK(t *testing.T, doc string) *Scenario {
	t.Helper()
	sc, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, doc)
	}
	return sc
}

func parseErr(t *testing.T, doc, substr string) {
	t.Helper()
	_, err := Parse([]byte(doc))
	if err == nil {
		t.Fatalf("expected error containing %q", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err.Error(), substr)
	}
}

const baseYAML = `name: demo
storage:
  - name: blackhole
    endpoint: http://127.0.0.1:8000
    protocol: http
    auth:
      type: none
dashboard:
  - name: dashboard-01
    endpoint: http://127.0.0.1:8001
    protocol: http
    auth:
      type: none
configs:
  min_size: 64
  max_size: 128
  concurrent: 1
  duration: 10
scenario:
  - name: linear-all
    time_stamp: 10
    type: linear
    rps_min_concurrent: 1
    rps_max_concurrent: 2
    error_rate: 0
    query_rate: 0
`

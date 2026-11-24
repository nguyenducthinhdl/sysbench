package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Report is one official hour appended to the phase 3 report.
type Report struct {
	Engine   string
	Scenario string
	Rows     int64
	Start    time.Time
	End      time.Time
	PromURL  string
	// DiskUsed is du -sb of the engine data directory after the hour.
	// DiskPreload is that directory after the preload has finished.
	// DiskBefore is the same directory before preload. Disk added is
	// DiskUsed minus DiskBefore.
	// ClickHouse: /var/lib/clickhouse. Elasticsearch: /usr/share/elasticsearch/data.
	// Loki: /tmp/loki. VictoriaLogs: /vlogs.
	DiskUsed       int64
	DiskPreload    int64
	DiskBefore     int64
	DiskSet        bool
	DiskPreloadSet bool
	DiskBeforeSet  bool
}

type sample struct {
	at    time.Time
	value float64
}

// AppendReport queries Prometheus and appends one markdown section to path.
func AppendReport(ctx context.Context, path string, rep Report) error {
	if rep.Engine == "" || rep.Scenario == "" {
		return fmt.Errorf("report: engine and scenario are required")
	}
	if rep.PromURL == "" {
		return fmt.Errorf("report: prometheus URL is required")
	}
	if rep.End.IsZero() {
		rep.End = time.Now().UTC()
	}
	if rep.Start.IsZero() {
		rep.Start = rep.End.Add(-time.Hour)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	instant := map[string]string{
		"ingest (events/s)": "sum(rate(bench_ingest_events_total[5m]))",
	}
	span := rangeSeconds(rep.Start, rep.End)
	instantAtEnd := map[string]string{
		"query avg (ms)":             "sum(increase(bench_query_duration_seconds_sum[" + span + "])) / sum(increase(bench_query_duration_seconds_count[" + span + "]))",
		"query avg full-text (ms)":   `sum(increase(bench_query_duration_seconds_sum{query_class="fulltext"}[` + span + `])) / sum(increase(bench_query_duration_seconds_count{query_class="fulltext"}[` + span + "]))",
		"query avg exact (ms)":       `sum(increase(bench_query_duration_seconds_sum{query_class="exact"}[` + span + `])) / sum(increase(bench_query_duration_seconds_count{query_class="exact"}[` + span + "]))",
		"query avg filter (ms)":      `sum(increase(bench_query_duration_seconds_sum{query_class="filter"}[` + span + `])) / sum(increase(bench_query_duration_seconds_count{query_class="filter"}[` + span + "]))",
		"query avg aggregation (ms)": `sum(increase(bench_query_duration_seconds_sum{query_class="aggregation"}[` + span + `])) / sum(increase(bench_query_duration_seconds_count{query_class="aggregation"}[` + span + "]))",
		"query p95 (ms)":             "histogram_quantile(0.95, sum by (le) (increase(bench_query_duration_seconds_bucket[" + span + "])))",
		"query p99 (ms)":             "histogram_quantile(0.99, sum by (le) (increase(bench_query_duration_seconds_bucket[" + span + "])))",
		"query p99 full-text (ms)":   `histogram_quantile(0.99, sum by (le) (increase(bench_query_duration_seconds_bucket{query_class="fulltext"}[` + span + "])))",
		"query p99 exact (ms)":       `histogram_quantile(0.99, sum by (le) (increase(bench_query_duration_seconds_bucket{query_class="exact"}[` + span + "])))",
		"query p99 filter (ms)":      `histogram_quantile(0.99, sum by (le) (increase(bench_query_duration_seconds_bucket{query_class="filter"}[` + span + "])))",
		"query p99 aggregation (ms)": `histogram_quantile(0.99, sum by (le) (increase(bench_query_duration_seconds_bucket{query_class="aggregation"}[` + span + "])))",
	}
	values := map[string]string{}
	for name, query := range instant {
		v, ok, err := promInstant(ctx, client, rep.PromURL, query)
		if err != nil {
			return fmt.Errorf("report: %s: %w", name, err)
		}
		if !ok {
			values[name] = "no series"
			continue
		}
		values[name] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	for name, query := range instantAtEnd {
		v, ok, err := promInstantAt(ctx, client, rep.PromURL, query, rep.End)
		if err != nil {
			return fmt.Errorf("report: %s: %w", name, err)
		}
		if !ok {
			values[name] = "no series"
			continue
		}
		values[name] = strconv.FormatFloat(v*1000, 'f', 3, 64)
	}
	values["After Preload Disk size (bytes)"] = "no series"
	if rep.DiskPreloadSet {
		values["After Preload Disk size (bytes)"] = strconv.FormatInt(rep.DiskPreload, 10)
	}
	if rep.DiskSet {
		values["Disk Storage (bytes)"] = strconv.FormatInt(rep.DiskUsed, 10)
	} else {
		values["Disk Storage (bytes)"] = "no series"
	}
	values["Disk added (bytes)"] = "no series"
	if rep.DiskSet && rep.DiskBeforeSet {
		values["Disk added (bytes)"] = strconv.FormatInt(rep.DiskUsed-rep.DiskBefore, 10)
	}
	values["Peak Used Mem (bytes)"] = "no series"
	if query := storageRAMQuery(rep.Engine, rangeMinutes(rep.Start, rep.End)); query != "" {
		v, ok, err := promInstantAt(ctx, client, rep.PromURL, query, rep.End)
		if err != nil {
			return fmt.Errorf("report: Peak Used Mem: %w", err)
		}
		if ok {
			values["Peak Used Mem (bytes)"] = strconv.FormatFloat(v, 'f', 0, 64)
		}
	}
	values["CPU Usage (cores)"] = "no series"
	if query := cpuUsageQuery(rep.Engine, rangeMinutes(rep.Start, rep.End)); query != "" {
		v, ok, err := promInstantAt(ctx, client, rep.PromURL, query, rep.End)
		if err != nil {
			return fmt.Errorf("report: CPU Usage: %w", err)
		}
		if ok {
			values["CPU Usage (cores)"] = strconv.FormatFloat(v, 'f', 3, 64)
		}
	}
	timeline, err := promRange(ctx, client, rep.PromURL, "sum(rate(bench_ingest_events_total[1m]))", rep.Start, rep.End, time.Minute)
	if err != nil {
		return fmt.Errorf("report: ingest timeline: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s / %s\n\n", rep.Engine, rep.Scenario)
	fmt.Fprintf(&b, "- Preload rows: %d\n", rep.Rows)
	fmt.Fprintf(&b, "- Window: %s to %s\n", rep.Start.UTC().Format(time.RFC3339), rep.End.UTC().Format(time.RFC3339))
	for _, name := range []string{"ingest (events/s)", "query avg (ms)", "query avg full-text (ms)", "query avg exact (ms)", "query avg filter (ms)", "query avg aggregation (ms)", "query p95 (ms)", "query p99 (ms)", "query p99 full-text (ms)", "query p99 exact (ms)", "query p99 filter (ms)", "query p99 aggregation (ms)", "Peak Used Mem (bytes)", "CPU Usage (cores)", "After Preload Disk size (bytes)", "Disk Storage (bytes)", "Disk added (bytes)"} {
		fmt.Fprintf(&b, "- %s: %s\n", name, values[name])
	}
	b.WriteString("\n### Ingest timeline\n\n")
	b.WriteString("| time | events/s |\n| --- | ---: |\n")
	if len(timeline) == 0 {
		b.WriteString("|  | no series |\n")
	}
	for _, s := range timeline {
		fmt.Fprintf(&b, "| %s | %s |\n", s.at.UTC().Format(time.RFC3339), strconv.FormatFloat(s.value, 'f', 2, 64))
	}
	b.WriteByte('\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// Two engines can finish a case at the same time. Hold the lock across the
	// header check and the section write so the bytes are not interleaved.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("report: lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		if _, err := io.WriteString(f, "# Phase 3 report\n\nEach section is one official hour. The table is the ingest rate across that hour.\n"); err != nil {
			return err
		}
	}
	_, err = io.WriteString(f, b.String())
	return err
}

func promInstant(ctx context.Context, client *http.Client, base, query string) (float64, bool, error) {
	return promInstantAt(ctx, client, base, query, time.Time{})
}

func promInstantAt(ctx context.Context, client *http.Client, base, query string, at time.Time) (float64, bool, error) {
	q := url.Values{"query": {query}}
	if !at.IsZero() {
		q.Set("time", strconv.FormatInt(at.Unix(), 10))
	}
	body, err := promGet(ctx, client, base, "/api/v1/query", q)
	if err != nil {
		return 0, false, err
	}
	return firstValue(body)
}

// storageRAMQuery is the peak bytes the storage process held during the window.
// Elasticsearch's resident-size gauge stays at 0, so that row is JVM heap plus non-heap.
func storageRAMQuery(engine string, minutes int) string {
	span := strconv.Itoa(minutes) + "m"
	switch engine {
	case "clickhouse":
		return "max(max_over_time(ClickHouseAsyncMetrics_MemoryResident[" + span + "]))"
	case "elasticsearch":
		return "max_over_time(sum(elasticsearch_jvm_memory_used_bytes)[" + span + ":1m])"
	case "loki":
		return `max(max_over_time(process_resident_memory_bytes{job="loki"}[` + span + "]))"
	case "victorialogs":
		return `max(max_over_time(process_resident_memory_bytes{job="victorialogs"}[` + span + "]))"
	default:
		return ""
	}
}

// cpuUsageQuery is the 99th of the 5-second process CPU samples, in cores.
// Prometheus scrapes every 5 seconds, so this is not a one-second series.
// One core is 1. ClickHouse uses its own CPU-time counter. Elasticsearch uses
// the node process counter. Loki and VictoriaLogs use the process collector.
func cpuUsageQuery(engine string, minutes int) string {
	span := strconv.Itoa(minutes) + "m:5s"
	switch engine {
	case "clickhouse":
		return "quantile_over_time(0.99, (irate(ClickHouseProfileEvents_OSCPUVirtualTimeMicroseconds[1m]) / 1e6)[" + span + "])"
	case "elasticsearch":
		return "quantile_over_time(0.99, irate(elasticsearch_process_cpu_seconds_total[1m])[" + span + "])"
	case "loki":
		return `quantile_over_time(0.99, sum(irate(process_cpu_seconds_total{job="loki"}[1m]))[` + span + "])"
	case "victorialogs":
		return `quantile_over_time(0.99, sum(irate(process_cpu_seconds_total{job="victorialogs"}[1m]))[` + span + "])"
	default:
		return ""
	}
}

func rangeSeconds(start, end time.Time) string {
	secs := int(end.Sub(start).Seconds())
	if secs < 1 {
		secs = 3600
	}
	return strconv.Itoa(secs) + "s"
}

func rangeMinutes(start, end time.Time) int {
	mins := int(end.Sub(start).Round(time.Minute) / time.Minute)
	if mins < 1 {
		return 60
	}
	return mins
}

func promRange(ctx context.Context, client *http.Client, base, query string, start, end time.Time, step time.Duration) ([]sample, error) {
	q := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatInt(int64(step.Seconds()), 10)},
	}
	body, err := promGet(ctx, client, base, "/api/v1/query_range", q)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Values [][]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "success" {
		if parsed.Error == "" {
			parsed.Error = parsed.Status
		}
		return nil, fmt.Errorf("%s", parsed.Error)
	}
	if len(parsed.Data.Result) == 0 {
		return nil, nil
	}
	var out []sample
	for _, pair := range parsed.Data.Result[0].Values {
		if len(pair) < 2 {
			continue
		}
		sec, ok := pair[0].(float64)
		if !ok {
			continue
		}
		val, ok, err := scalar(pair[1])
		if err != nil || !ok {
			continue
		}
		out = append(out, sample{at: time.Unix(int64(sec), 0).UTC(), value: val})
	}
	return out, nil
}

func promGet(ctx context.Context, client *http.Client, base, path string, q url.Values) ([]byte, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus status %d", resp.StatusCode)
	}
	return body, nil
}

func firstValue(body []byte) (float64, bool, error) {
	var parsed struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, false, err
	}
	if parsed.Status != "success" {
		if parsed.Error == "" {
			parsed.Error = parsed.Status
		}
		return 0, false, fmt.Errorf("%s", parsed.Error)
	}
	if len(parsed.Data.Result) == 0 || len(parsed.Data.Result[0].Value) < 2 {
		return 0, false, nil
	}
	return scalar(parsed.Data.Result[0].Value[1])
}

func scalar(v any) (float64, bool, error) {
	switch n := v.(type) {
	case string:
		if n == "NaN" {
			return 0, false, nil
		}
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false, err
		}
		return f, true, nil
	case float64:
		return n, true, nil
	default:
		return 0, false, fmt.Errorf("value type %T", v)
	}
}

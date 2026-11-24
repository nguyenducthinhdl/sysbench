package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppendReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "query_range") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"values":[[1700000000,"100"],[1700000300,"1000"]]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"12.5"]}]}}`))
	}))
	defer srv.Close()
	path := t.TempDir() + "/phase3-report.md"
	start := time.Unix(1700000000, 0).UTC()
	err := AppendReport(context.Background(), path, Report{
		Engine:         "clickhouse",
		Scenario:       "phase3-steady",
		Rows:           100000000,
		Start:          start,
		End:            start.Add(time.Hour),
		PromURL:        srv.URL,
		DiskUsed:       12345,
		DiskPreload:    8000,
		DiskBefore:     1000,
		DiskSet:        true,
		DiskPreloadSet: true,
		DiskBeforeSet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"# Phase 3 report", "## clickhouse / phase3-steady", "Preload rows: 100000000", "1000.00", "query avg (ms): 12500.000", "query avg full-text (ms): 12500.000", "query avg exact (ms): 12500.000", "query avg filter (ms): 12500.000", "query avg aggregation (ms): 12500.000", "query p99 full-text (ms): 12500.000", "query p99 exact (ms): 12500.000", "query p99 filter (ms): 12500.000", "query p99 aggregation (ms): 12500.000", "Peak Used Mem (bytes): 12", "CPU Usage (cores): 12.500", "After Preload Disk size (bytes): 8000", "Disk Storage (bytes): 12345", "Disk added (bytes): 11345"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q\n%s", want, text)
		}
	}
}

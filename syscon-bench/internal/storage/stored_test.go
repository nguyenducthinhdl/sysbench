package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStoredCounts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sql := string(body)
		switch {
		case strings.Contains(sql, "CREATE"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(sql, "count()"):
			_, _ = io.WriteString(w, "100000000\n")
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/logs/_count", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"count":42}`)
	})
	mux.HandleFunc("/_index_template/syscon-logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/loki/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"7"]}]}}`)
	})
	mux.HandleFunc("/select/logsql/stats_query", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"9"]}]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ch, err := NewClickHouse(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	n, err := ch.Stored(context.Background())
	if err != nil || n != 100000000 {
		t.Fatalf("clickhouse %d %v", n, err)
	}
	es, err := NewElasticsearch(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	n, err = es.Stored(context.Background())
	if err != nil || n != 42 {
		t.Fatalf("elasticsearch %d %v", n, err)
	}
	lk, err := NewLoki(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	n, err = lk.Stored(context.Background())
	if err != nil || n != 7 {
		t.Fatalf("loki %d %v", n, err)
	}
	vl, err := NewVictoriaLogs(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	n, err = vl.Stored(context.Background())
	if err != nil || n != 9 {
		t.Fatalf("victorialogs %d %v", n, err)
	}
}

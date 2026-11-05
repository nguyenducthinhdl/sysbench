package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestVictoriaLogsPushAndSearch(t *testing.T) {
	var mu sync.Mutex
	var user, pass, ctype string
	var insertQuery string
	var lines []Event
	var forms []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		user, pass = u, p
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/insert/") {
			mu.Lock()
			ctype = r.Header.Get("Content-Type")
			insertQuery = r.URL.RawQuery
			mu.Unlock()
			for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
				var ev Event
				if err := json.Unmarshal([]byte(line), &ev); err != nil {
					t.Errorf("line: %v", err)
					http.Error(w, "bad", http.StatusBadRequest)
					return
				}
				mu.Lock()
				lines = append(lines, ev)
				mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("form: %v", err)
		}
		mu.Lock()
		forms = append(forms, form)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"count()"},"value":[1,"8"]}]}}`)
	}))
	defer srv.Close()

	vl, err := NewVictoriaLogs(srv.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	ev := sampleEvent()
	if err := vl.Push(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := vl.Push(context.Background(), []Event{ev}); err != nil {
		t.Fatal(err)
	}
	res, err := vl.Search(context.Background(), Query{Class: QueryFullText, Token: "checkout"})
	if err != nil || res.Hits != 8 {
		t.Fatalf("hits %d err %v", res.Hits, err)
	}
	if _, err := vl.Search(context.Background(), Query{Class: QueryExact, Field: "trace-id", Value: ev.TraceID}); err != nil {
		t.Fatal(err)
	}
	if _, err := vl.Search(context.Background(), filterQuery()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if user != "admin" || pass != "admin" || ctype != "application/stream+json" {
		t.Fatalf("auth %s/%s type %s", user, pass, ctype)
	}
	decoded, err := url.QueryUnescape(insertQuery)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decoded, "_msg_field=message") || !strings.Contains(decoded, "_time_field=time") || !strings.Contains(decoded, "log-severity") {
		t.Fatalf("insert %s", decoded)
	}
	if len(lines) != 1 || lines[0].Message != ev.Message || lines[0].Time != ev.Time {
		t.Fatalf("%+v", lines)
	}
	if len(forms) != 3 {
		t.Fatalf("forms %d", len(forms))
	}
	if forms[0].Get("query") != `"checkout" | stats count() as hits` || forms[0].Get("start") != "" {
		t.Fatalf("fulltext %+v", forms[0])
	}
	if !strings.Contains(forms[1].Get("query"), `"trace-id":="`+ev.TraceID+`"`) {
		t.Fatalf("exact %s", forms[1].Get("query"))
	}
	if !strings.Contains(forms[2].Get("query"), `"service":="checkout"`) || forms[2].Get("start") == "" || forms[2].Get("end") == "" {
		t.Fatalf("filter %+v", forms[2])
	}
}

func TestVictoriaLogsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	vl, err := NewVictoriaLogs(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := vl.Search(context.Background(), Query{Class: QueryExact, Field: "service", Value: "checkout"})
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusInternalServerError {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(string(res.Body), "nope") {
		t.Fatalf("%q", res.Body)
	}
	if _, err := vl.Search(context.Background(), Query{Class: QueryFilter, Field: "uuid", Value: "abc"}); err == nil {
		t.Fatal("expected field error")
	}
}

func TestNewVictoriaLogsRejectsRelativeURL(t *testing.T) {
	if _, err := NewVictoriaLogs("://bad", "", ""); err == nil {
		t.Fatal("expected error")
	}
}

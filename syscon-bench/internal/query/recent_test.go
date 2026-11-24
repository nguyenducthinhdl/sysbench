package query

import (
	"math/rand/v2"
	"testing"
	"time"

	"syscon-bench/internal/storage"
)

func TestRecentRingKeepsFirstTime(t *testing.T) {
	r := NewRecent(2)
	if r.Len() != 0 {
		t.Fatal(r.Len())
	}
	r.Add(nil)
	r.Add([]storage.Event{
		{Message: "aaaa bbbb", Service: "alpha", Time: "2026-11-02T00:00:01.000Z", StatusCode: 200, LogSeverity: "info", UUID: "a", TraceID: "ta"},
	})
	r.Add([]storage.Event{
		{Message: "cccc dddd", Service: "beta", Time: "2026-11-02T00:00:02.000Z", StatusCode: 500, LogSeverity: "error", UUID: "b", TraceID: "tb"},
	})
	r.Add([]storage.Event{
		{Message: "eeee ffff", Service: "gamma", Time: "2026-11-02T00:00:03.000Z", StatusCode: 200, LogSeverity: "info", UUID: "c", TraceID: "tc"},
	})
	if r.Len() != 2 {
		t.Fatalf("len %d", r.Len())
	}
	first, ok := r.get(0)
	if !ok || first.service != "beta" {
		t.Fatalf("oldest %+v", first)
	}
	second, ok := r.get(1)
	if !ok || second.service != "gamma" {
		t.Fatalf("newest %+v", second)
	}
	wantFirst := time.Date(2026, 11, 2, 0, 0, 1, 0, time.UTC)
	if !first.first.Equal(wantFirst) || !second.first.Equal(wantFirst) {
		t.Fatalf("first %s", first.first)
	}
	if _, ok := r.get(2); ok {
		t.Fatal("index 2")
	}
	if len(first.tokens) != 2 || first.tokens[0] != "cccc" {
		t.Fatalf("tokens %q", first.tokens)
	}
}

func TestPickFullTextUsesTheRareToken(t *testing.T) {
	r := NewRecent(4)
	for i := 0; i < 10000; i++ {
		msg := "booking accepted INFO request"
		if i == 0 {
			msg = "booking accepted raretermzzzz"
		}
		r.Add([]storage.Event{{
			Message: msg, Time: "2026-11-02T00:00:01.000Z", StatusCode: 200, LogSeverity: "info",
		}})
	}
	rng := rand.New(rand.NewPCG(1, 1))
	tok, ok := r.pickFullText(rng, []string{"booking", "accepted", "raretermzzzz"})
	if !ok || tok != "raretermzzzz" {
		t.Fatalf("token %q ok %v", tok, ok)
	}
	if _, ok := r.pickFullText(rng, []string{"booking", "accepted"}); ok {
		t.Fatal("head tokens were searched")
	}
}

func TestParseTime(t *testing.T) {
	got, ok := parseTime("2026-11-02T11:24:00.000Z")
	if !ok || !got.Equal(time.Date(2026, 11, 2, 11, 24, 0, 0, time.UTC)) {
		t.Fatalf("%s %v", got, ok)
	}
	if _, ok := parseTime("not-a-time"); ok {
		t.Fatal("accepted garbage")
	}
}

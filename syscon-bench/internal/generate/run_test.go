package generate

import (
	"context"
	"errors"
	"io"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"syscon-bench/internal/query"
	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

func TestRunManualClock(t *testing.T) {
	sc := flatScenario(10, 50, 0)
	clock := newManualClock()
	sink := &memSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Run(ctx, sc, sink, Options{
		BatchSize:     16,
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scheduled != 500 || res.Accepted != 500 || res.Failed != 0 {
		t.Fatalf("%+v", res)
	}
	if int(res.MessageBytes) != 500*64 {
		t.Fatalf("bytes %d", res.MessageBytes)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	seen := map[string]struct{}{}
	for _, ev := range sink.events {
		if ev.StatusCode != 200 || ev.LogSeverity != "info" || len(ev.Message) != 64 {
			t.Fatalf("event status %d sev %s len %d", ev.StatusCode, ev.LogSeverity, len(ev.Message))
		}
		if _, ok := seen[ev.UUID]; ok {
			t.Fatalf("duplicate %s", ev.UUID)
		}
		seen[ev.UUID] = struct{}{}
	}
}

func TestRunSpikeIgnoresMinRate(t *testing.T) {
	sc := &scenario.Scenario{
		Name:    "spike",
		Storage: scenario.Target{Name: "blackhole", Endpoint: "http://127.0.0.1:9", Protocol: "http"},
		Configs: scenario.Configs{MinSize: 64, MaxSize: 64, Concurrent: 4, Duration: 2},
		Segments: []scenario.Segment{{
			Name: "spike-03", Start: 0, TimeStamp: 2, Type: "spike",
			RPSMin: 2000, RPSMax: 100, ErrorRate: 1,
		}},
	}
	clock := newManualClock()
	sink := &memSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Run(ctx, sc, sink, Options{
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          3,
		Logger:        log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scheduled != 200 || res.Accepted != 200 {
		t.Fatalf("%+v", res)
	}
	for _, ev := range sink.events {
		if ev.StatusCode != 500 || ev.LogSeverity != "error" {
			t.Fatalf("status %d sev %s", ev.StatusCode, ev.LogSeverity)
		}
	}
}

func TestRunTicks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sc := flatScenario(30, 0, 0)
	clock := newManualClock()
	var mid, final int
	_, err := Run(ctx, sc, &memSink{}, Options{
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		TickInterval:  15 * time.Second,
		Tick: func(_ context.Context, isFinal bool) {
			if isFinal {
				final++
			} else {
				mid++
			}
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if mid != 1 || final != 1 {
		t.Fatalf("30s ticks mid=%d final=%d", mid, final)
	}

	sc31 := flatScenario(31, 0, 0)
	clock = newManualClock()
	mid, final = 0, 0
	_, err = Run(ctx, sc31, &memSink{}, Options{
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		TickInterval:  15 * time.Second,
		Tick: func(_ context.Context, isFinal bool) {
			if isFinal {
				final++
			} else {
				mid++
			}
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if mid != 2 || final != 1 {
		t.Fatalf("31s ticks mid=%d final=%d", mid, final)
	}
}

func TestRunFailedSink(t *testing.T) {
	sc := flatScenario(1, 20, 0)
	clock := newManualClock()
	sink := &memSink{err: errors.New("down")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Run(ctx, sc, sink, Options{
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scheduled != 20 || res.Accepted != 0 || res.Failed != 20 || res.MessageBytes != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestRunCancel(t *testing.T) {
	sc := flatScenario(30, 10, 0)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, err := Run(ctx, sc, &memSink{}, Options{
		FlushInterval: time.Hour,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
	})
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not stop the clock")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if res.Scheduled >= 300 {
		t.Fatalf("scheduled %d", res.Scheduled)
	}
	if res.Accepted+res.Failed != res.Scheduled {
		t.Fatalf("%+v", res)
	}
}

func TestRunAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Run(ctx, flatScenario(10, 100, 0), &memSink{}, Options{Logger: log.New(io.Discard, "", 0)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if res.Scheduled != 0 {
		t.Fatalf("scheduled %d", res.Scheduled)
	}
}

func TestRunQuerySchedule(t *testing.T) {
	sc := &scenario.Scenario{
		Name:    "q",
		Storage: scenario.Target{Name: "blackhole", Endpoint: "http://127.0.0.1:9", Protocol: "http"},
		Configs: scenario.Configs{MinSize: 64, MaxSize: 64, Concurrent: 1, Duration: 2},
		Segments: []scenario.Segment{
			{Name: "a", Start: 0, TimeStamp: 1, Type: "spike", RPSMax: 0, QueryRate: 60},
			{Name: "b", Start: 1, TimeStamp: 2, Type: "spike", RPSMax: 10, QueryRate: 60},
		},
	}
	clock := newManualClock()
	var mu sync.Mutex
	var got []time.Duration
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Run(ctx, sc, &memSink{}, Options{
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
		QueryAt: func(_ context.Context, runStart, now time.Time) {
			mu.Lock()
			got = append(got, now.Sub(runStart))
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scheduled != 10 || res.Queries != 2 {
		t.Fatalf("%+v", res)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != 2 || got[0] != 500*time.Millisecond || got[1] != 1500*time.Millisecond {
		t.Fatalf("dues %v", got)
	}

	quiet := flatScenario(2, 10, 0)
	var n atomic.Int32
	res, err = Run(ctx, quiet, &memSink{}, Options{
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
		QueryAt: func(context.Context, time.Time, time.Time) {
			n.Add(1)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 0 || res.Queries != 0 || res.Scheduled != 20 {
		t.Fatalf("zero rate queries %d scheduled %d", n.Load(), res.Scheduled)
	}
}

func TestRunQueryFinishesBeforeFinalTick(t *testing.T) {
	sc := flatScenario(2, 0, 0)
	sc.Segments[0].QueryRate = 60
	clock := newManualClock()
	release := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	defer unlock()
	var mu sync.Mutex
	var order []string
	var started atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, sc, &memSink{}, Options{
			FlushInterval: time.Hour,
			Now:           clock.Now,
			Sleep:         clock.Sleep,
			Seed:          1,
			Logger:        log.New(io.Discard, "", 0),
			QueryAt: func(context.Context, time.Time, time.Time) {
				started.Add(1)
				<-release
				mu.Lock()
				order = append(order, "done")
				mu.Unlock()
			},
			Tick: func(context.Context, bool) {
				mu.Lock()
				order = append(order, "tick")
				mu.Unlock()
			},
		})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if started.Load() != 2 {
		t.Fatalf("started %d", started.Load())
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[2] != "tick" || order[0] != "done" || order[1] != "done" {
		t.Fatalf("order %v", order)
	}
}

func TestRunQueryUsesAcceptedEvents(t *testing.T) {
	sc := flatScenario(1, 20, 0)
	sc.Segments[0].QueryRate = 60
	clock := newManualClock()
	sink := &memSink{}
	recent := query.NewRecent(64)
	capture := &captureQuerier{}
	client := &query.Client{
		Querier: capture,
		Recent:  recent,
		Logger:  log.New(io.Discard, "", 0),
		Seed:    1,
		Timeout: time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Run(ctx, sc, sink, Options{
		BatchSize:     1,
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
		Accepted:      recent.Add,
		QueryAt: func(ctx context.Context, runStart, now time.Time) {
			deadline := time.Now().Add(2 * time.Second)
			for recent.Len() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			client.Execute(ctx, runStart, now)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scheduled != 20 || res.Accepted != 20 || res.Queries != 1 {
		t.Fatalf("%+v", res)
	}
	got := capture.snapshot()
	if len(got) != 1 {
		t.Fatalf("searches %d", len(got))
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !queryUsesAccepted(got[0], sink.events) {
		t.Fatalf("query %+v not from accepted events", got[0])
	}

	failed := &memSink{err: errors.New("down")}
	recent = query.NewRecent(8)
	capture = &captureQuerier{}
	client.Recent = recent
	client.Querier = capture
	sc = flatScenario(1, 10, 0)
	sc.Segments[0].QueryRate = 60
	clock = newManualClock()
	res, err = Run(ctx, sc, failed, Options{
		BatchSize:     1,
		FlushInterval: time.Hour,
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		Seed:          1,
		Logger:        log.New(io.Discard, "", 0),
		Accepted:      recent.Add,
		QueryAt: func(ctx context.Context, runStart, now time.Time) {
			client.Execute(ctx, runStart, now)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 0 || res.Queries != 1 || len(capture.snapshot()) != 0 {
		t.Fatalf("skip %+v searches %d", res, len(capture.snapshot()))
	}
}

func queryUsesAccepted(q storage.Query, events []storage.Event) bool {
	switch q.Class {
	case storage.QueryFullText:
		for _, ev := range events {
			for _, tok := range query.Tokens(ev.Message) {
				if tok == q.Token {
					return q.Field == "" && q.From.IsZero()
				}
			}
		}
	case storage.QueryExact:
		if !q.From.IsZero() || !q.To.IsZero() {
			return false
		}
		return valueSeen(q.Field, q.Value, events)
	case storage.QueryFilter:
		if !q.From.Before(q.To) {
			return false
		}
		return valueSeen(q.Field, q.Value, events)
	case storage.QueryAggregation:
		return q.From.Before(q.To)
	}
	return false
}

func valueSeen(field, value string, events []storage.Event) bool {
	for _, ev := range events {
		switch field {
		case "service":
			if ev.Service == value {
				return true
			}
		case "trace-id":
			if ev.TraceID == value {
				return true
			}
		case "uuid":
			if ev.UUID == value {
				return true
			}
		case "status-code":
			if (value == "200" && ev.StatusCode == 200) || (value == "500" && ev.StatusCode == 500) {
				return true
			}
		case "log-severity":
			if ev.LogSeverity == value {
				return true
			}
		}
	}
	return false
}

type captureQuerier struct {
	mu  sync.Mutex
	got []storage.Query
}

func (c *captureQuerier) Search(_ context.Context, q storage.Query) (storage.Result, error) {
	c.mu.Lock()
	c.got = append(c.got, q)
	c.mu.Unlock()
	return storage.Result{Hits: 2, Body: []byte("[]")}, nil
}

func (c *captureQuerier) snapshot() []storage.Query {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]storage.Query, len(c.got))
	copy(out, c.got)
	return out
}

func flatScenario(seconds, rps float64, errorRate float64) *scenario.Scenario {
	return &scenario.Scenario{
		Name:    "flat",
		Storage: scenario.Target{Name: "blackhole", Endpoint: "http://127.0.0.1:9", Protocol: "http"},
		Configs: scenario.Configs{MinSize: 64, MaxSize: 64, Concurrent: 4, Duration: seconds},
		Segments: []scenario.Segment{{
			Name: "all", Start: 0, TimeStamp: seconds, Type: "linear",
			RPSMin: rps, RPSMax: rps, ErrorRate: errorRate,
		}},
	}
}

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock() *manualClock {
	return &manualClock{t: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type memSink struct {
	mu     sync.Mutex
	events []storage.Event
	err    error
}

func (m *memSink) Push(_ context.Context, batch []storage.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, batch...)
	return nil
}

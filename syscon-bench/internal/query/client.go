// Package query runs the search loop on the same scenario clock as ingest.
//
// One process-wide ticker draws fulltext, exact, and filter at 30/30/40.
// Each search uses a value from an event the sink has already accepted.
// A tick with nothing accepted yet does not search and does not count as a
// query error. Latency is how long Querier.Search takes, and Search reads
// the body to the end before it returns. A fully read body, including a
// non-2xx status, is the histogram sample. Timeouts, non-2xx statuses, and
// transport errors increment bench_query_errors_total. Canceling the run does
// not.
package query

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"syscon-bench/internal/storage"
)

const (
	// DefaultTimeout bounds one search. It matches the top histogram bucket.
	DefaultTimeout = 60 * time.Second
	filterLookback = 60 * time.Second
)

var (
	exactFields  = []string{"service", "trace-id", "uuid", "status-code"}
	filterFields = []string{"service", "log-severity", "status-code"}
)

// Observer records one finished search.
// *metrics.Metrics implements it.
type Observer interface {
	ObserveQuery(class string, seconds float64, bodyRead, failed bool)
}

// Client builds a query and calls Querier.
// Seed is read on the first Execute. 0 draws a seed from the wall clock.
// Logger and Timeout are read on each search; set them before Execute.
type Client struct {
	Querier storage.Querier
	Recent  *Recent
	Metrics Observer
	Logger  *log.Logger
	Seed    uint64
	Timeout time.Duration

	mu  sync.Mutex
	rng *rand.Rand
}

// Execute runs the tick at scenario time now. runStart is the clock at the
// beginning of the run. It is safe to call from overlapping ticks.
func (c *Client) Execute(ctx context.Context, runStart, now time.Time) {
	if c == nil || c.Querier == nil || ctx.Err() != nil {
		return
	}
	q, ok := c.plan(runStart, now)
	if !ok {
		return
	}
	c.run(ctx, q)
}

func (c *Client) plan(runStart, now time.Time) (storage.Query, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()
	if c.Recent == nil || c.Recent.Len() == 0 {
		return storage.Query{}, false
	}
	class := pickClass(c.rng)
	n := c.Recent.Len()
	start := c.rng.IntN(n)
	for k := 0; k < n; k++ {
		snap, ok := c.Recent.get((start + k) % n)
		if !ok {
			continue
		}
		q, ok := makeQuery(c.rng, class, snap, runStart, now)
		if ok {
			return q, true
		}
	}
	return storage.Query{}, false
}

func (c *Client) initLocked() {
	if c.rng != nil {
		return
	}
	seed := c.Seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
		if seed == 0 {
			seed = 1
		}
	}
	c.rng = rand.New(rand.NewPCG(seed, 1))
}

func (c *Client) run(ctx context.Context, q storage.Query) {
	if c == nil || c.Querier == nil || ctx.Err() != nil {
		return
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	res, err := c.Querier.Search(cctx, q)
	seconds := time.Since(start).Seconds()
	bodyRead, failed, canceled := classify(err)
	if canceled {
		return
	}
	if c.Metrics != nil {
		c.Metrics.ObserveQuery(string(q.Class), seconds, bodyRead, failed)
	}
	c.logResult(q, res, err, bodyRead)
}

func (c *Client) logResult(q storage.Query, res storage.Result, err error, bodyRead bool) {
	lg := c.logger()
	switch {
	case bodyRead && err != nil:
		lg.Printf("query class=%s %s hits=%d error=%v", q.Class, describe(q), res.Hits, err)
	case bodyRead:
		lg.Printf("query class=%s %s hits=%d", q.Class, describe(q), res.Hits)
	default:
		lg.Printf("query class=%s %s error=%v", q.Class, describe(q), err)
	}
}

func (c *Client) logger() *log.Logger {
	if c != nil && c.Logger != nil {
		return c.Logger
	}
	return log.Default()
}

// pickClass draws the fixed mix: 30 full-text, 30 exact, 40 filter.
// One hundred integer buckets keep the split exact.
func pickClass(rng *rand.Rand) storage.QueryClass {
	return classFor(rng.IntN(100))
}

func classFor(n int) storage.QueryClass {
	switch {
	case n < 30:
		return storage.QueryFullText
	case n < 60:
		return storage.QueryExact
	default:
		return storage.QueryFilter
	}
}

func makeQuery(rng *rand.Rand, class storage.QueryClass, snap snapshot, runStart, now time.Time) (storage.Query, bool) {
	switch class {
	case storage.QueryFullText:
		if len(snap.tokens) == 0 {
			return storage.Query{}, false
		}
		return storage.Query{
			Class: storage.QueryFullText,
			Token: snap.tokens[rng.IntN(len(snap.tokens))],
		}, true
	case storage.QueryExact:
		field, value, ok := chooseField(rng, exactFields, snap)
		if !ok {
			return storage.Query{}, false
		}
		return storage.Query{Class: storage.QueryExact, Field: field, Value: value}, true
	case storage.QueryFilter:
		field, value, ok := chooseField(rng, filterFields, snap)
		if !ok {
			return storage.Query{}, false
		}
		from, to := filterWindow(now, runStart, snap.first)
		return storage.Query{
			Class: storage.QueryFilter,
			Field: field,
			Value: value,
			From:  from,
			To:    to,
		}, true
	default:
		return storage.Query{}, false
	}
}

func chooseField(rng *rand.Rand, fields []string, snap snapshot) (string, string, bool) {
	if len(fields) == 0 {
		return "", "", false
	}
	start := rng.IntN(len(fields))
	for n := 0; n < len(fields); n++ {
		field := fields[(start+n)%len(fields)]
		value := snap.value(field)
		if value != "" {
			return field, value, true
		}
	}
	return "", "", false
}

// filterWindow is [T-60s, T). While the run is younger than 60 seconds the
// lower bound moves forward to the first accepted event time, and only when
// that still leaves a non-empty half-open window.
func filterWindow(now, runStart, first time.Time) (time.Time, time.Time) {
	from := now.Add(-filterLookback)
	young := !runStart.IsZero() && now.Sub(runStart) < filterLookback
	if young && !first.IsZero() && first.After(from) && first.Before(now) {
		from = first
	}
	return from.UTC(), now.UTC()
}

func describe(q storage.Query) string {
	switch q.Class {
	case storage.QueryFullText:
		return "token=" + q.Token
	case storage.QueryFilter:
		return fmt.Sprintf("field=%s value=%s from=%s to=%s",
			q.Field, q.Value,
			q.From.UTC().Format(time.RFC3339Nano),
			q.To.UTC().Format(time.RFC3339Nano))
	default:
		return fmt.Sprintf("field=%s value=%s", q.Field, q.Value)
	}
}

// classify reports whether the body was fully read and whether the search failed.
// A canceled run is neither: the histogram and the error counter stay unchanged.
func classify(err error) (bodyRead, failed, canceled bool) {
	if err == nil {
		return true, false, false
	}
	var status *storage.StatusError
	if errors.As(err, &status) {
		return true, true, false
	}
	if isTimeout(err) {
		return false, true, false
	}
	if errors.Is(err, context.Canceled) {
		return false, false, true
	}
	return false, true, false
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

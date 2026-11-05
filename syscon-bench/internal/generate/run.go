// Package generate walks a scenario clock and pushes log batches through a Sink.
//
// Workers share one process-wide offered rate. concurrent is how many of them
// issue events. If the sink falls behind, due events are still attempted as
// soon as a worker is free; the schedule does not drop them or lower the rate.
// error_rate marks log-severity error and status-code 500. The HTTP call still sends
// those events. Query ticks share this clock. Each due search starts in its own
// goroutine so a slow search does not stall ingest, and Run waits for those
// calls before the final Tick.
package generate

import (
	"context"
	"errors"
	"log"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"syscon-bench/internal/query"
	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

const (
	// DefaultBatchSize is how many events a worker hands to one Sink.Push.
	DefaultBatchSize = 64
	// DefaultFlushInterval pushes a short batch so a low rate still reaches the sink.
	DefaultFlushInterval = time.Second
)

// Options controls pacing and the dashboard tick. Zero values use defaults.
type Options struct {
	BatchSize     int
	FlushInterval time.Duration
	// Now is the wall clock used for event timestamps and, together with Sleep,
	// the scenario clock. Nil uses time.Now.
	Now func() time.Time
	// Sleep waits during the scenario clock. Nil uses an interruptible timer.
	// A test clock's Sleep should advance the same clock Now reads.
	Sleep func(time.Duration)
	// Seed is the rng seed. 0 draws one from the wall clock.
	Seed uint64
	// TickInterval is how often Tick is called in scenario time.
	// Zero suppresses intermediate calls. The final call still runs.
	TickInterval time.Duration
	// Tick is called from the scheduler goroutine as scenario time crosses
	// each TickInterval boundary strictly before the end, and once more with
	// final set after workers have finished in-flight pushes and query calls.
	Tick    func(ctx context.Context, final bool)
	Logger  *log.Logger
	Catalog *Catalog
	// Accepted is called with each batch the sink accepted.
	// The slice is only valid for the call. The query client samples it.
	Accepted func([]storage.Event)
	// QueryAt is one query tick. now is the scenario clock at the due time
	// and runStart is the clock when the run began. Each call is its own
	// goroutine and may overlap. Nil disables the query loop.
	// In-flight calls finish before the final Tick.
	QueryAt func(ctx context.Context, runStart, now time.Time)
}

// Result counts the events handed to workers and what the sink did with them.
type Result struct {
	Scheduled    int64
	Accepted     int64
	Failed       int64
	MessageBytes int64
	// Queries is how many query ticks were started. A tick that finds no
	// accepted event does not search and is still counted here.
	Queries int64
}

type runner struct {
	sc        *scenario.Scenario
	sink      storage.Sink
	opt       Options
	start     time.Time
	seed      uint64
	limit     time.Duration
	nextTick  time.Duration
	seq       atomic.Uint64
	scheduled atomic.Int64
	accepted  atomic.Int64
	failed    atomic.Int64
	bytes     atomic.Int64
	queries   atomic.Int64
	queryWG   sync.WaitGroup
	qDue      []time.Duration
	jobs      chan job
	catalog   *Catalog
}

type job struct {
	seg int
}

// Run schedules every offered event and waits for in-flight pushes to finish.
// A cancelled context stops new events and new queries. Events already queued
// are still pushed, and searches already started still finish. Those calls
// finish before the final Tick.
func Run(ctx context.Context, sc *scenario.Scenario, sink storage.Sink, opt Options) (Result, error) {
	if sc == nil {
		return Result{}, errors.New("generate: nil scenario")
	}
	if sink == nil {
		return Result{}, errors.New("generate: nil sink")
	}
	if sc.Configs.Concurrent < 1 {
		return Result{}, errors.New("generate: concurrent must be >= 1")
	}
	opt = opt.withDefaults()
	r := newRunner(sc, sink, opt)
	r.qDue = planQueries(sc, r.limit, opt.QueryAt != nil)
	r.logf("scenario=%s storage=%s endpoint=%s workers=%d duration=%gs offered=%d queries=%d batch=%d seed=%d",
		sc.Name, sc.Storage.Name, sc.Storage.Endpoint, sc.Configs.Concurrent, sc.Configs.Duration,
		TotalOffered(sc), len(r.qDue), opt.BatchSize, r.seed)

	var wg sync.WaitGroup
	for i := 0; i < sc.Configs.Concurrent; i++ {
		wg.Add(1)
		w := &worker{
			rng: rand.New(rand.NewPCG(r.seed, uint64(i+1))),
			r:   r,
		}
		go w.run(&wg)
	}
	err := r.schedule(ctx)
	close(r.jobs)
	wg.Wait()
	r.queryWG.Wait()
	if r.opt.Tick != nil {
		r.opt.Tick(ctx, true)
	}
	res := r.result()
	if err != nil {
		r.logf("scenario=%s stopped: %v (scheduled=%d accepted=%d failed=%d message_bytes=%d queries=%d)",
			sc.Name, err, res.Scheduled, res.Accepted, res.Failed, res.MessageBytes, res.Queries)
		return res, err
	}
	elapsed := r.now().Sub(r.start)
	if elapsed > r.limit+time.Second {
		r.logf("scenario clock overran by %s", elapsed-r.limit)
	}
	r.logf("scenario=%s finished scheduled=%d accepted=%d failed=%d message_bytes=%d queries=%d",
		sc.Name, res.Scheduled, res.Accepted, res.Failed, res.MessageBytes, res.Queries)
	return res, nil
}

func (o Options) withDefaults() Options {
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = DefaultFlushInterval
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	return o
}

func newRunner(sc *scenario.Scenario, sink storage.Sink, opt Options) *runner {
	seed := opt.Seed
	if seed == 0 {
		seed = uint64(time.Now().UnixNano())
		if seed == 0 {
			seed = 1
		}
	}
	buf := opt.BatchSize * sc.Configs.Concurrent
	if buf < 1 {
		buf = 1
	}
	start := opt.Now()
	limit := seconds(sc.Configs.Duration)
	next := opt.TickInterval
	if next < 0 {
		next = 0
	}
	return &runner{
		sc:       sc,
		sink:     sink,
		opt:      opt,
		start:    start,
		seed:     seed,
		limit:    limit,
		nextTick: next,
		jobs:     make(chan job, buf),
		catalog:  opt.Catalog,
	}
}

func (r *runner) result() Result {
	return Result{
		Scheduled:    r.scheduled.Load(),
		Accepted:     r.accepted.Load(),
		Failed:       r.failed.Load(),
		MessageBytes: r.bytes.Load(),
		Queries:      r.queries.Load(),
	}
}

func (r *runner) logf(format string, args ...any) {
	r.opt.Logger.Printf(format, args...)
}

func (r *runner) now() time.Time {
	return r.opt.Now()
}

func (r *runner) schedule(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	di := 0
	// Queries due at or before an event start first, still on this clock.
	// The final pass uses a half-open end so nothing starts at duration.
	fireUntil := func(limit time.Duration, inclusive bool) error {
		for di < len(r.qDue) {
			if inclusive {
				if r.qDue[di] > limit {
					break
				}
			} else if r.qDue[di] >= limit {
				break
			}
			if err := r.sleepUntil(ctx, r.qDue[di]); err != nil {
				return err
			}
			r.startQuery(ctx)
			di++
		}
		return nil
	}
	for si := range r.sc.Segments {
		seg := r.sc.Segments[si]
		n := Offered(seg)
		for i := 0; i < n; i++ {
			due := seconds(EventDue(seg, i))
			if err := fireUntil(due, true); err != nil {
				return err
			}
			if err := r.sleepUntil(ctx, due); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case r.jobs <- job{seg: si}:
				r.scheduled.Add(1)
			}
		}
	}
	if err := fireUntil(r.limit, false); err != nil {
		return err
	}
	return r.sleepUntil(ctx, r.limit)
}

func (r *runner) startQuery(ctx context.Context) {
	if r.opt.QueryAt == nil {
		return
	}
	now := r.now()
	start := r.start
	r.queries.Add(1)
	r.queryWG.Add(1)
	go func() {
		defer r.queryWG.Done()
		r.opt.QueryAt(ctx, start, now)
	}()
}

func planQueries(sc *scenario.Scenario, limit time.Duration, enabled bool) []time.Duration {
	if !enabled || sc == nil {
		return nil
	}
	dues := query.Schedule(sc)
	out := make([]time.Duration, 0, len(dues))
	for _, due := range dues {
		d := seconds(due)
		if limit > 0 && d >= limit {
			d = limit - time.Nanosecond
		}
		if d < 0 {
			d = 0
		}
		out = append(out, d)
	}
	return out
}

func (r *runner) sleepUntil(ctx context.Context, target time.Duration) error {
	for {
		elapsed := r.now().Sub(r.start)
		if elapsed >= target {
			r.intermediateTicks(ctx, elapsed)
			return nil
		}
		wake := target
		if r.opt.Tick != nil && r.opt.TickInterval > 0 && r.nextTick > elapsed && r.nextTick < r.limit && r.nextTick < wake {
			wake = r.nextTick
		}
		if err := r.sleep(ctx, wake-elapsed); err != nil {
			return err
		}
		r.intermediateTicks(ctx, r.now().Sub(r.start))
	}
}

func (r *runner) intermediateTicks(ctx context.Context, elapsed time.Duration) {
	if r.opt.Tick == nil || r.opt.TickInterval <= 0 {
		return
	}
	for r.nextTick < r.limit && elapsed >= r.nextTick {
		r.opt.Tick(ctx, false)
		r.nextTick += r.opt.TickInterval
	}
}

func (r *runner) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.opt.Sleep != nil {
		r.opt.Sleep(d)
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *runner) push(ctx context.Context, batch []storage.Event) {
	if len(batch) == 0 {
		return
	}
	if err := r.sink.Push(ctx, batch); err != nil {
		r.failed.Add(int64(len(batch)))
		r.logf("sink: %v (events=%d)", err, len(batch))
		return
	}
	var n int64
	for _, ev := range batch {
		n += int64(len(ev.Message))
	}
	r.accepted.Add(int64(len(batch)))
	r.bytes.Add(n)
	if r.opt.Accepted != nil {
		r.opt.Accepted(batch)
	}
}

type worker struct {
	rng   *rand.Rand
	r     *runner
	batch []storage.Event
}

func (w *worker) run(wg *sync.WaitGroup) {
	defer wg.Done()
	w.batch = make([]storage.Event, 0, w.r.opt.BatchSize)
	ticker := time.NewTicker(w.r.opt.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case j, ok := <-w.r.jobs:
			if !ok {
				w.flush()
				return
			}
			w.batch = append(w.batch, w.build(j))
			if len(w.batch) >= w.r.opt.BatchSize {
				w.flush()
			}
		case <-ticker.C:
			w.flush()
		}
	}
}

func (w *worker) build(j job) storage.Event {
	seg := w.r.sc.Segments[j.seg]
	return newEvent(w.rng, w.r.catalog, w.r.sc.Configs.MinSize, w.r.sc.Configs.MaxSize, seg, w.r.seq.Add(1), w.r.now())
}

func (w *worker) flush() {
	if len(w.batch) == 0 {
		return
	}
	send := w.batch
	w.batch = make([]storage.Event, 0, cap(send))
	w.r.push(context.Background(), send)
}

func seconds(s float64) time.Duration {
	return time.Duration(math.Round(s * float64(time.Second)))
}

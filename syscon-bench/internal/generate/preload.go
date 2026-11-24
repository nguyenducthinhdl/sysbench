package generate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"time"

	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

// PreloadOptions controls a row-count preload. The scenario clock does not run
// and client metrics are not pushed. Stored is asked before each batch.
type PreloadOptions struct {
	Rows      int64
	Batch     int
	MinSize   int
	MaxSize   int
	ErrorRate float64
	Seed      uint64
	Catalog   *Catalog
	Logger    *log.Logger
}

// Preload pushes contract events until Stored reports at least Rows.
// A push error stops the load. If the engine stays short after more pushes
// than Rows, Preload returns the short count.
func Preload(ctx context.Context, sink storage.Sink, stored func(context.Context) (int64, error), opt PreloadOptions) (int64, error) {
	if sink == nil || stored == nil {
		return 0, fmt.Errorf("generate: preload needs a sink and a row count")
	}
	if opt.Rows < 1 {
		return 0, fmt.Errorf("generate: preload rows must be >= 1")
	}
	if opt.Batch < 1 {
		opt.Batch = 200
	}
	if opt.MinSize == 0 {
		opt.MinSize = 100
	}
	if opt.MaxSize == 0 {
		opt.MaxSize = 200000
	}
	if opt.MinSize < 64 || opt.MaxSize > 1048576 || opt.MinSize > opt.MaxSize {
		return 0, fmt.Errorf("generate: message size %d..%d is outside [64, 1048576]", opt.MinSize, opt.MaxSize)
	}
	if opt.ErrorRate < 0 || opt.ErrorRate > 1 {
		return 0, fmt.Errorf("generate: error_rate must be in [0, 1]")
	}
	if opt.Seed == 0 {
		opt.Seed = 1
	}
	if opt.Logger == nil {
		opt.Logger = log.Default()
	}
	rng := rand.New(rand.NewPCG(opt.Seed, 1))
	seg := scenario.Segment{ErrorRate: opt.ErrorRate}
	var pushed int64
	var have int64
	for {
		if err := ctx.Err(); err != nil {
			return have, err
		}
		n, err := stored(ctx)
		if err != nil {
			return have, err
		}
		have = n
		if have >= opt.Rows {
			opt.Logger.Printf("preload stored=%d want=%d pushed=%d", have, opt.Rows, pushed)
			return have, nil
		}
		if pushed > opt.Rows && have < opt.Rows {
			return have, fmt.Errorf("generate: preload stored %d rows after %d pushes, want %d", have, pushed, opt.Rows)
		}
		need := opt.Rows - pushed
		if need > int64(opt.Batch) {
			need = int64(opt.Batch)
		}
		if need < 1 {
			need = 1
		}
		batch := make([]storage.Event, 0, need)
		now := time.Now().UTC()
		for i := int64(0); i < need; i++ {
			batch = append(batch, newEvent(rng, opt.Catalog, opt.MinSize, opt.MaxSize, seg, uint64(pushed+i+1), now))
		}
		if err := sink.Push(ctx, batch); err != nil {
			return have, fmt.Errorf("generate: preload push: %w", err)
		}
		pushed += int64(len(batch))
	}
}

// PreloadFile pushes the JSONL at path. The file must contain exactly rows
// events, and the store must be empty. Stored is polled after the push until
// it reaches rows.
func PreloadFile(ctx context.Context, sink storage.Sink, stored func(context.Context) (int64, error), path string, batch int, rows int64, settle time.Duration, logger *log.Logger) (int64, error) {
	if sink == nil || stored == nil {
		return 0, fmt.Errorf("generate: preload needs a sink and a row count")
	}
	if path == "" {
		return 0, fmt.Errorf("generate: preload file is required")
	}
	if rows < 1 {
		return 0, fmt.Errorf("generate: preload rows must be >= 1")
	}
	if batch < 1 {
		batch = 1000
	}
	if logger == nil {
		logger = log.Default()
	}
	if settle <= 0 {
		settle = 60 * time.Second
	}
	lines, err := countLines(path)
	if err != nil {
		return 0, err
	}
	if lines != rows {
		return 0, fmt.Errorf("generate: preload file has %d rows, want %d", lines, rows)
	}
	have, err := stored(ctx)
	if err != nil {
		return 0, err
	}
	if have >= rows {
		logger.Printf("preload stored=%d want=%d file=%s", have, rows, path)
		return have, nil
	}
	if have > 0 {
		return have, fmt.Errorf("generate: preload file needs an empty store, found %d rows", have)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("generate: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	batchEv := make([]storage.Event, 0, batch)
	var pushed int64
	flush := func() error {
		if len(batchEv) == 0 {
			return nil
		}
		if err := sink.Push(ctx, batchEv); err != nil {
			return fmt.Errorf("generate: preload push: %w", err)
		}
		pushed += int64(len(batchEv))
		if pushed%100000 == 0 || pushed == rows {
			logger.Printf("preload pushed=%d/%d", pushed, rows)
		}
		batchEv = batchEv[:0]
		return nil
	}
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return have, err
		}
		if len(sc.Bytes()) == 0 {
			continue
		}
		var ev storage.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return have, fmt.Errorf("generate: preload file line %d: %w", pushed+int64(len(batchEv))+1, err)
		}
		batchEv = append(batchEv, ev)
		if len(batchEv) == batch {
			if err := flush(); err != nil {
				return have, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return have, fmt.Errorf("generate: %w", err)
	}
	if err := flush(); err != nil {
		return have, err
	}
	if pushed != rows {
		return have, fmt.Errorf("generate: preload pushed %d rows, want %d", pushed, rows)
	}
	deadline := time.Now().Add(settle)
	for {
		if err := ctx.Err(); err != nil {
			return have, err
		}
		n, err := stored(ctx)
		if err != nil {
			return have, err
		}
		have = n
		if have >= rows {
			logger.Printf("preload stored=%d want=%d pushed=%d", have, rows, pushed)
			return have, nil
		}
		wait := time.Second
		if remain := time.Until(deadline); remain < wait {
			wait = remain
		}
		if wait <= 0 {
			return have, fmt.Errorf("generate: preload stored %d rows after %d pushes, want %d", have, pushed, rows)
		}
		select {
		case <-ctx.Done():
			return have, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func countLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("generate: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var n int64
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return n, fmt.Errorf("generate: %w", err)
	}
	return n, nil
}

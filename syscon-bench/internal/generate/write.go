package generate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"syscon-bench/internal/scenario"
)

// WriteResult is one preload file. Bytes is the file size, not message bytes.
// WriteFile does not start a scenario clock and does not push metrics.
type WriteResult struct {
	Events int64
	Bytes  int64
}

// WriteFile writes contract events, one JSON object per line, until the file
// is at least target bytes. Identities are the pool of 1000. Message bodies
// follow the format and source mix. minSize and maxSize are message bytes.
func WriteFile(path string, target int64, minSize, maxSize int, errorRate float64, seed uint64) (WriteResult, error) {
	if path == "" {
		return WriteResult{}, errors.New("generate: write path is required")
	}
	if target < 1 {
		return WriteResult{}, errors.New("generate: write target must be >= 1 byte")
	}
	if minSize < 64 || maxSize > 1048576 || minSize > maxSize {
		return WriteResult{}, fmt.Errorf("generate: message size %d..%d is outside [64, 1048576]", minSize, maxSize)
	}
	if errorRate < 0 || errorRate > 1 {
		return WriteResult{}, errors.New("generate: error_rate must be in [0, 1]")
	}
	if seed == 0 {
		seed = 1
	}
	f, err := os.Create(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	rng := rand.New(rand.NewPCG(seed, 1))
	seg := scenario.Segment{ErrorRate: errorRate}
	now := time.Now().UTC()
	var events int64
	var written int64
	for written < target {
		ev := newEvent(rng, nil, minSize, maxSize, seg, uint64(events), now)
		line, err := json.Marshal(ev)
		if err != nil {
			return WriteResult{}, fmt.Errorf("generate: encode: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			return WriteResult{}, fmt.Errorf("generate: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			return WriteResult{}, fmt.Errorf("generate: %w", err)
		}
		written += int64(len(line) + 1)
		events++
		if len(ev.Message) < minSize || len(ev.Message) > maxSize {
			return WriteResult{}, fmt.Errorf("generate: message length %d outside %d..%d", len(ev.Message), minSize, maxSize)
		}
	}
	if err := w.Flush(); err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	if err := f.Close(); err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	return WriteResult{Events: events, Bytes: info.Size()}, nil
}

// WriteRows writes exactly rows contract events, one JSON object per line.
// A catalog draws identities and messages the same way a run does. A nil
// catalog uses the synthetic message sized between minSize and maxSize.
// The same seed rewrites the same file.
func WriteRows(path string, rows int64, cat *Catalog, minSize, maxSize int, errorRate float64, seed uint64) (WriteResult, error) {
	if path == "" {
		return WriteResult{}, errors.New("generate: write path is required")
	}
	if rows < 1 {
		return WriteResult{}, errors.New("generate: write rows must be >= 1")
	}
	if errorRate < 0 || errorRate > 1 {
		return WriteResult{}, errors.New("generate: error_rate must be in [0, 1]")
	}
	if cat == nil {
		if minSize < 64 || maxSize > 1048576 || minSize > maxSize {
			return WriteResult{}, fmt.Errorf("generate: message size %d..%d is outside [64, 1048576]", minSize, maxSize)
		}
	} else if len(cat.Messages) == 0 || len(cat.InstanceIDs) == 0 || len(cat.IPs) == 0 || len(cat.Hosts) == 0 || len(cat.Services) == 0 {
		return WriteResult{}, errors.New("generate: catalog is missing field values or messages")
	}
	if seed == 0 {
		seed = 1
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return WriteResult{}, fmt.Errorf("generate: %w", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	rng := rand.New(rand.NewPCG(seed, 1))
	seg := scenario.Segment{ErrorRate: errorRate}
	// Fixed clock so the same seed and catalog rewrite the same file.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var written int64
	for i := int64(1); i <= rows; i++ {
		ev := newEvent(rng, cat, minSize, maxSize, seg, uint64(i), now)
		line, err := json.Marshal(ev)
		if err != nil {
			return WriteResult{}, fmt.Errorf("generate: encode: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			return WriteResult{}, fmt.Errorf("generate: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			return WriteResult{}, fmt.Errorf("generate: %w", err)
		}
		written += int64(len(line) + 1)
		if i%100000 == 0 {
			log.Printf("write rows=%d/%d", i, rows)
		}
	}
	if err := w.Flush(); err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	if err := f.Close(); err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("generate: %w", err)
	}
	if info.Size() != written {
		return WriteResult{}, fmt.Errorf("generate: wrote %d bytes, stat %d", written, info.Size())
	}
	return WriteResult{Events: rows, Bytes: info.Size()}, nil
}

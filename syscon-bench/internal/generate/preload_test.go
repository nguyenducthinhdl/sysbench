package generate

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"syscon-bench/internal/storage"
)

type countSink struct {
	n int64
}

func (s *countSink) Push(_ context.Context, batch []storage.Event) error {
	s.n += int64(len(batch))
	return nil
}

func TestPreloadStopsOnStoredRows(t *testing.T) {
	sink := &countSink{}
	got, err := Preload(context.Background(), sink, func(context.Context) (int64, error) {
		return sink.n, nil
	}, PreloadOptions{
		Rows:      25,
		Batch:     10,
		MinSize:   64,
		MaxSize:   64,
		ErrorRate: 0,
		Seed:      1,
		Logger:    log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got < 25 || sink.n < 25 {
		t.Fatalf("stored %d pushed %d", got, sink.n)
	}
}

func TestPreloadFilePushesTheSameLines(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/rows.jsonl"
	cat := &Catalog{
		InstanceIDs: []string{"instance-0001"},
		IPs:         []string{"10.0.0.1"},
		Hosts:       []string{"host-0001"},
		Services:    []string{"users-0001"},
		Messages:    []string{"hello"},
	}
	if _, err := WriteRows(path, 5, cat, 0, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	sink := &countSink{}
	got, err := PreloadFile(context.Background(), sink, func(context.Context) (int64, error) {
		return sink.n, nil
	}, path, 2, 5, time.Second, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 || sink.n != 5 {
		t.Fatalf("stored %d pushed %d", got, sink.n)
	}
}

func TestPreloadFileRejectsAShortFile(t *testing.T) {
	path := t.TempDir() + "/rows.jsonl"
	if _, err := WriteRows(path, 2, nil, 64, 64, 0, 1); err != nil {
		t.Fatal(err)
	}
	_, err := PreloadFile(context.Background(), &countSink{}, func(context.Context) (int64, error) {
		return 0, nil
	}, path, 2, 5, time.Millisecond, log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(err.Error(), "has 2 rows") {
		t.Fatal(err)
	}
}

func TestPreloadRefusesAShortEngine(t *testing.T) {
	sink := &countSink{}
	_, err := Preload(context.Background(), sink, func(context.Context) (int64, error) {
		return 0, nil
	}, PreloadOptions{
		Rows:      5,
		Batch:     2,
		MinSize:   64,
		MaxSize:   64,
		ErrorRate: 0,
		Seed:      1,
		Logger:    log.New(io.Discard, "", 0),
	})
	if err == nil {
		t.Fatal("expected a short count")
	}
}

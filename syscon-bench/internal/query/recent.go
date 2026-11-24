package query

import (
	"math"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"syscon-bench/internal/storage"
)

const (
	// DefaultRecent is how many accepted events the query client remembers.
	// The ring keeps the fields a search needs, not the message body.
	DefaultRecent = 4096
	// maxTokens bounds the full-text sample taken from one message.
	maxTokens = 32
	// fullTextHeadRate is the document rate above which a token is too common
	// to use as a full-text search. 1% of accepted events.
	fullTextHeadRate = 0.01
	// fullTextRareRate is the document rate a full-text search aims for.
	// 0.01% of accepted events.
	fullTextRareRate = 0.0001
	// fullTextMinDocs is how many events are counted before the head filter applies.
	fullTextMinDocs = 1000
)

// Recent is the accepted-event sample the query client draws from.
// Add and Get are safe for concurrent use.
type Recent struct {
	mu    sync.Mutex
	buf   []record
	next  int
	count int
	first time.Time
	docs  int
	df    map[string]int
}

type record struct {
	tokens   []string
	service  string
	traceID  string
	uuid     string
	status   string
	severity string
}

type snapshot struct {
	tokens   []string
	service  string
	traceID  string
	uuid     string
	status   string
	severity string
	first    time.Time
}

// NewRecent returns an empty sample. n <= 0 uses DefaultRecent.
func NewRecent(n int) *Recent {
	if n <= 0 {
		n = DefaultRecent
	}
	return &Recent{buf: make([]record, n), df: map[string]int{}}
}

// Add records a batch the sink accepted. An empty batch is ignored.
// The first parsed event time stays the run's first accepted time, including
// after the ring overwrites that event.
func (r *Recent) Add(batch []storage.Event) {
	if r == nil || len(batch) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range batch {
		if r.first.IsZero() {
			if t, ok := parseTime(ev.Time); ok {
				r.first = t
			}
		}
		tokens := collectTokens(ev.Message, maxTokens)
		r.noteTokens(tokens)
		r.buf[r.next] = record{
			tokens:   tokens,
			service:  ev.Service,
			traceID:  ev.TraceID,
			uuid:     ev.UUID,
			status:   strconv.Itoa(ev.StatusCode),
			severity: ev.LogSeverity,
		}
		r.next = (r.next + 1) % len(r.buf)
		if r.count < len(r.buf) {
			r.count++
		}
	}
}

// Len is the number of events currently stored.
func (r *Recent) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// get returns logical index i. Index 0 is the oldest event still stored.
func (r *Recent) get(i int) (snapshot, bool) {
	if r == nil {
		return snapshot{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= r.count {
		return snapshot{}, false
	}
	return r.snapshot(i), true
}

func (r *Recent) snapshot(i int) snapshot {
	rec := r.buf[r.slot(i)]
	return snapshot{
		tokens:   rec.tokens,
		service:  rec.service,
		traceID:  rec.traceID,
		uuid:     rec.uuid,
		status:   rec.status,
		severity: rec.severity,
		first:    r.first,
	}
}

// noteTokens counts each token once per event. Caller holds r.mu.
func (r *Recent) noteTokens(tokens []string) {
	r.docs++
	seen := map[string]struct{}{}
	for _, tok := range tokens {
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		r.df[tok]++
	}
}

// pickFullText chooses a token near a 0.01% document rate.
// Tokens seen in more than 1% of accepted events are skipped once 1000 events
// have been counted. Caller does not hold r.mu.
func (r *Recent) pickFullText(rng *rand.Rand, tokens []string) (string, bool) {
	if r == nil || len(tokens) == 0 {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.docs == 0 {
		return "", false
	}
	best := ""
	bestDist := math.MaxFloat64
	ties := 0
	seen := map[string]struct{}{}
	for _, tok := range tokens {
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		n := r.df[tok]
		if n == 0 {
			continue
		}
		rate := float64(n) / float64(r.docs)
		if r.docs >= fullTextMinDocs && rate > fullTextHeadRate {
			continue
		}
		dist := math.Abs(rate - fullTextRareRate)
		if best == "" || dist < bestDist {
			best = tok
			bestDist = dist
			ties = 1
			continue
		}
		if dist == bestDist {
			ties++
			if rng.IntN(ties) == 0 {
				best = tok
			}
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

func (r *Recent) slot(i int) int {
	if r.count < len(r.buf) {
		return i
	}
	return (r.next + i) % len(r.buf)
}

// parseTime reads the event time field. The generator writes UTC RFC3339
// with milliseconds.
func parseTime(value string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func (s snapshot) value(field string) string {
	switch field {
	case "service":
		return s.service
	case "trace-id":
		return s.traceID
	case "uuid":
		return s.uuid
	case "status-code":
		return s.status
	case "log-severity":
		return s.severity
	default:
		return ""
	}
}

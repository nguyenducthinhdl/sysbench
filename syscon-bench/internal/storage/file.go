package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
)

// maxFileBody is the largest accepted request. A full batch of 1 MiB messages
// fits under this cap.
const maxFileBody = 128 << 20

// FileLog is a mock storage that appends each accepted event as one JSON line.
type FileLog struct {
	path string
	mu   sync.Mutex
	f    *os.File
	n    atomic.Int64
}

// OpenFileLog creates or appends to path. Close flushes the file.
func OpenFileLog(path string) (*FileLog, error) {
	if path == "" {
		return nil, fmt.Errorf("file: path is required")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}
	return &FileLog{path: path, f: f}, nil
}

// Path is the file events are appended to.
func (l *FileLog) Path() string { return l.path }

// Count is the number of events appended since this process opened the file.
func (l *FileLog) Count() int64 { return l.n.Load() }

// Close flushes and closes the file.
func (l *FileLog) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.f.Close()
	l.f = nil
	if err != nil {
		return fmt.Errorf("file: %w", err)
	}
	return nil
}

// Append writes events as JSON lines. A later line never tears an earlier one.
func (l *FileLog) Append(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return fmt.Errorf("file: encode: %w", err)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return fmt.Errorf("file: closed")
	}
	if _, err := l.f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("file: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("file: %w", err)
	}
	l.n.Add(int64(len(events)))
	return nil
}

// Handler accepts the generator's JSON array and appends each event.
// GET reports how many events this process has stored. When user is non-empty,
// POST and PUT require that basic auth.
func (l *FileLog) Handler(user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, "events %d\n", l.Count())
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if user != "" {
			gotUser, gotPass, ok := r.BasicAuth()
			if !ok || gotUser != user || gotPass != pass {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("WWW-Authenticate", `Basic realm="syscon-bench"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxFileBody+1))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if len(body) > maxFileBody {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		var batch []Event
		if err := json.Unmarshal(body, &batch); err != nil {
			http.Error(w, "expected a JSON array of events", http.StatusBadRequest)
			return
		}
		if err := l.Append(batch); err != nil {
			http.Error(w, "write failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

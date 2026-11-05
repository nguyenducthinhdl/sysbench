// Blackhole is the Phase 1 HTTP discard target.
//
// The generator POSTs a JSON array of events. The handler reads the body and
// answers 204. Basic auth is sent when the scenario sets it, and checked by
// the handler when a username is configured.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	blackholeTimeout = 60 * time.Second
	// maxSearchBody is how much of a search response is retained.
	// The rest is still read so the caller waits until EOF.
	maxSearchBody = 1 << 20
)

var _ Querier = (*Blackhole)(nil)

// Blackhole posts batches to an HTTP endpoint and treats any 2xx as success.
type Blackhole struct {
	endpoint string
	user     string
	pass     string
	client   *http.Client
}

// NewBlackhole builds a client for an absolute http or https URL.
// user is empty when the scenario auth type is none.
func NewBlackhole(endpoint, user, pass string) (*Blackhole, error) {
	u, err := url.Parse(endpoint)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("blackhole: endpoint must be an absolute http or https URL")
	}
	return &Blackhole{
		endpoint: endpoint,
		user:     user,
		pass:     pass,
		client:   newClient(blackholeTimeout),
	}, nil
}

// Push encodes batch as a JSON array and POSTs it.
// An empty batch is a no-op. Non-2xx, timeout, and transport errors fail the batch.
func (b *Blackhole) Push(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("blackhole: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("blackhole: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "syscon-bench")
	if b.user != "" {
		req.SetBasicAuth(b.user, b.pass)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("blackhole: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("blackhole: status %d", resp.StatusCode)
	}
	return nil
}

// Search GETs the endpoint and reads the body to the end.
// There is no index, so Hits is 0. The query is sent as URL parameters
// so the call is still the search the client built. A non-2xx status is a
// *StatusError after the body has been read.
func (b *Blackhole) Search(ctx context.Context, q Query) (Result, error) {
	u, err := url.Parse(b.endpoint)
	if err != nil {
		return Result{}, fmt.Errorf("blackhole: endpoint: %w", err)
	}
	vals := u.Query()
	vals.Set("class", string(q.Class))
	if q.Token != "" {
		vals.Set("q", q.Token)
	}
	if q.Field != "" {
		vals.Set("field", q.Field)
		vals.Set("value", q.Value)
	}
	if !q.From.IsZero() {
		vals.Set("from", q.From.UTC().Format(time.RFC3339Nano))
		vals.Set("to", q.To.UTC().Format(time.RFC3339Nano))
	}
	u.RawQuery = vals.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("blackhole: request: %w", err)
	}
	req.Header.Set("User-Agent", "syscon-bench")
	if b.user != "" {
		req.SetBasicAuth(b.user, b.pass)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("blackhole: %w", err)
	}
	defer resp.Body.Close()
	body, err := readSearchBody(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("blackhole: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{Body: body}, fmt.Errorf("blackhole: %w", &StatusError{Status: resp.StatusCode})
	}
	return Result{Hits: 0, Body: body}, nil
}

// readSearchBody reads until EOF so latency includes the full response.
// Only the first maxSearchBody bytes are kept.
func readSearchBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSearchBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSearchBody {
		if _, err := io.Copy(io.Discard, r); err != nil {
			return nil, err
		}
		b = b[:maxSearchBody]
	}
	return b, nil
}

// Handler accepts POST and PUT bodies and discards them.
// When user is non-empty, the request must carry that basic auth.
func Handler(user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok\n")
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
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func newClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   1024,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableCompression:    true,
		},
	}
}

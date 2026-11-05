package metrics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// Pusher posts the bench series to the dashboard endpoint.
type Pusher struct {
	Endpoint string
	Username string
	Password string
	Metrics  *Metrics
	Client   *http.Client
	Logger   *log.Logger

	mu sync.Mutex
	wg sync.WaitGroup
}

// At posts the series. Intermediate calls return after starting the post.
// The final call waits for those posts, then posts once more.
// At is called from one goroutine. A failed post is logged and does not
// change the ingest or query error counters.
func (p *Pusher) At(ctx context.Context, final bool) {
	if p == nil || p.Endpoint == "" || p.Metrics == nil {
		return
	}
	if !final {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.push(ctx)
		}()
		return
	}
	p.wg.Wait()
	p.push(ctx)
}

func (p *Pusher) push(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	client := p.Client
	if client == nil {
		client = newPushClient()
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := Push(cctx, client, p.Endpoint, p.Username, p.Password, p.Metrics); err != nil && p.Logger != nil {
		p.Logger.Printf("dashboard push: %v", err)
	}
}

// Push POSTs the exposition body. Non-2xx and transport errors are returned.
func Push(ctx context.Context, client *http.Client, endpoint, user, pass string, m *Metrics) error {
	if client == nil {
		client = newPushClient()
	}
	body, err := m.Text()
	if err != nil {
		return fmt.Errorf("dashboard: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("dashboard: request: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)
	req.Header.Set("User-Agent", "syscon-bench")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dashboard: status %d", resp.StatusCode)
	}
	return nil
}

func newPushClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			MaxIdleConns:        4,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  true,
		},
	}
}

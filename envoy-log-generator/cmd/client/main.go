// Command client offers every routing point to Envoy.
//
// CLIENT_COUNT processes together send RouteRPS on each route. This process
// sends RouteRPS/CLIENT_COUNT so adding a client does not multiply the rate.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"envoy-log-generator/internal/logx"
	"envoy-log-generator/internal/schedule"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := schedule.FromEnv()
	if err != nil {
		return err
	}
	name := env("CLIENT_NAME", "client")
	count, err := strconv.Atoi(env("CLIENT_COUNT", "1"))
	if err != nil || count < 1 {
		return fmt.Errorf("CLIENT_COUNT must be a positive integer")
	}
	base := strings.TrimRight(env("ENVOY_URL", "http://127.0.0.1:10000"), "/")
	lg, err := logx.Open(env("LOG_PATH", "logs/"+name+".log"), name, logx.DetectIP())
	if err != nil {
		return err
	}
	defer lg.Close()

	started := time.Now()
	go panicLater(cfg, lg)
	go logPace(cfg, lg, count)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 256,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
	}
	// Each routing point gets the same offered rate, staggered so the bursts
	// do not line up.
	eps := endpoints()
	for i, ep := range eps {
		ep := ep
		stagger := time.Duration(i) * 5 * time.Millisecond
		if i == len(eps)-1 {
			pace(cfg, lg, client, base, ep, count, started, stagger)
			return nil
		}
		go pace(cfg, lg, client, base, ep, count, started, stagger)
	}
	return nil
}

type endpoint struct {
	method string
	path   func() string
	body   func() []byte
}

func endpoints() []endpoint {
	return []endpoint{
		{http.MethodPost, staticPath("/payments"), paymentsBody},
		{http.MethodPost, staticPath("/booking"), bookingBody},
		{http.MethodGet, func() string { return "/bookings/" + ident("bk") }, nil},
		{http.MethodGet, func() string { return "/payments/" + ident("pay") }, nil},
		{http.MethodGet, func() string { return "/users/" + ident("usr") }, nil},
		{http.MethodPut, func() string { return "/order/" + ident("ord") }, orderBody},
		{http.MethodOptions, staticPath("/bookings"), nil},
		{http.MethodPost, staticPath("/foods/data"), foodsBody},
	}
}

func staticPath(path string) func() string {
	return func() string { return path }
}

func panicLater(cfg schedule.Config, lg *logx.Logger) {
	delay := cfg.PanicDelay()
	lg.Info("runtime", "panic scheduled", delay.String())
	time.Sleep(delay)
	lg.Panic("runtime", "scheduled panic")
}

func logPace(cfg schedule.Config, lg *logx.Logger, count int) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		total := cfg.RouteRPS(now)
		share := total / float64(count)
		lg.Debug("pace", "target rps", fmt.Sprintf("per_route=%.2f this_client=%.2f", total, share))
	}
}

// pace emits n requests across each wall-clock second without waiting for
// the previous response, so a slow Envoy does not silently lower the offer.
func pace(cfg schedule.Config, lg *logx.Logger, client *http.Client, base string, ep endpoint, count int, started time.Time, stagger time.Duration) {
	if stagger > 0 {
		time.Sleep(stagger)
	}
	sem := make(chan struct{}, 128)
	for {
		n := int(math.Round(cfg.RouteRPS(time.Now()) / float64(count)))
		if n < 1 {
			n = 1
		}
		second := time.Now()
		interval := time.Second / time.Duration(n)
		for i := 0; i < n; i++ {
			slot := second.Add(time.Duration(i) * interval)
			if d := time.Until(slot); d > 0 {
				time.Sleep(d)
			}
			var body []byte
			if ep.body != nil {
				body = ep.body()
			}
			path := ep.path()
			method := ep.method
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				send(lg, client, method, base+path, body, started)
			}()
		}
		if d := time.Until(second.Add(time.Second)); d > 0 {
			time.Sleep(d)
		}
	}
}

func send(lg *logx.Logger, client *http.Client, method, rawURL string, payload []byte, started time.Time) {
	starting := time.Since(started) < 45*time.Second
	attempts := 1
	if starting {
		attempts = 8
	}
	var last error
	for i := 0; i < attempts; i++ {
		var reader io.Reader
		if len(payload) > 0 {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, rawURL, reader)
		if err != nil {
			lg.Error("http", method+" failed reason="+transportReason(err), err.Error())
			return
		}
		if len(payload) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			last = err
			if starting && i+1 < attempts {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			lg.Error("http", method+" "+req.URL.Path+" failed reason="+transportReason(err), err.Error())
			return
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		recorded := string(payload)
		if recorded == "" {
			recorded = req.URL.Path
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			lg.Info("http", fmt.Sprintf("%s %s -> %d", method, req.URL.Path, resp.StatusCode), recorded)
			return
		}
		reason := failureReason(resp.StatusCode, string(respBody))
		msg := fmt.Sprintf("%s %s -> %d reason=%s", method, req.URL.Path, resp.StatusCode, reason)
		lg.Error("http", msg, string(respBody))
		return
	}
	if last != nil {
		lg.Error("http", method+" failed reason="+transportReason(last), last.Error())
	}
}

// failureReason is why this response was not processed.
// A backend body that starts with "reason: ..." wins. Otherwise the status
// supplies the cause, and a short response line fills in the rest.
func failureReason(status int, body string) string {
	if tagged := taggedReason(body); tagged != "" {
		return tagged
	}
	switch status {
	case http.StatusTooManyRequests:
		return "rate limit exceeded"
	case http.StatusServiceUnavailable:
		if line := firstLine(body); line != "" {
			return line
		}
		return "upstream unavailable"
	case http.StatusGatewayTimeout:
		return "upstream timeout"
	case http.StatusNotFound:
		return "route not found"
	case http.StatusMethodNotAllowed:
		return "method not allowed"
	}
	if line := firstLine(body); line != "" {
		return line
	}
	if status >= 500 {
		return "backend processing failed"
	}
	return "request rejected"
}

func transportReason(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return "request timeout"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"):
		return "envoy unreachable"
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"):
		return "request timeout"
	default:
		return "request failed"
	}
}

func taggedReason(body string) string {
	line := firstLine(body)
	const prefix = "reason: "
	if strings.HasPrefix(line, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	return ""
}

func firstLine(body string) string {
	line := strings.TrimSpace(body)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if line == "" || strings.HasPrefix(line, "goroutine ") {
		return ""
	}
	if len(line) > 180 {
		line = line[:180]
	}
	return line
}

func paymentsBody() []byte {
	return fmt.Appendf(nil, `{"order_id":"%s","amount":%.2f,"currency":"%s"}`,
		ident("ord"),
		1+rand.Float64()*200,
		[]string{"USD", "EUR", "VND"}[rand.Intn(3)],
	)
}

func bookingBody() []byte {
	return fmt.Appendf(nil, `{"booking_id":"%s","route":"%s","passengers":%d}`,
		ident("bk"),
		[]string{"SGN-HAN", "HAN-DAD", "SGN-PQC"}[rand.Intn(3)],
		rand.Intn(4)+1,
	)
}

func orderBody() []byte {
	return fmt.Appendf(nil, `{"status":"%s"}`, []string{"confirmed", "cancelled", "pending"}[rand.Intn(3)])
}

func foodsBody() []byte {
	return fmt.Appendf(nil, `{"item":"%s","qty":%d}`,
		[]string{"pho", "banh-mi", "com-tam"}[rand.Intn(3)],
		rand.Intn(3)+1,
	)
}

func ident(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, rand.Intn(90000)+10000)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

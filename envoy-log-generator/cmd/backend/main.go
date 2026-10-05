// Command backend serves one service behind Envoy.
//
// SERVICE selects which routes this process accepts. ErrorRate is the chance
// a request returns HTTP 500 with a reason and a Go stack trace. A separate
// timer panics the process so the supervisor can restart it.
package main

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"runtime/debug"
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
	service := os.Getenv("SERVICE")
	if _, ok := serviceEndpoints[service]; !ok {
		return fmt.Errorf("SERVICE must be one of payments, booking, users, orders, foods")
	}
	name := env("NAME", service)
	lg, err := logx.Open(env("LOG_PATH", "logs/"+service+".log"), name, logx.DetectIP())
	if err != nil {
		return err
	}
	defer lg.Close()

	go panicLater(cfg, lg)

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           &handler{cfg: cfg, log: lg, service: service},
		ReadHeaderTimeout: 2 * time.Second,
	}
	return srv.ListenAndServe()
}

func panicLater(cfg schedule.Config, lg *logx.Logger) {
	delay := cfg.PanicDelay()
	lg.Info("runtime", "panic scheduled", delay.String())
	time.Sleep(delay)
	lg.Panic("runtime", "scheduled panic")
}

type handler struct {
	cfg     schedule.Config
	log     *logx.Logger
	service string
}

// endpoint is one method and path this service accepts.
// exact paths match the whole path. Other paths also match subpaths,
// so GET /payments/ matches /payments/pay-100.
type endpoint struct {
	method string
	path   string
	exact  bool
}

var serviceEndpoints = map[string][]endpoint{
	"payments": {
		{http.MethodPost, "/payments", false},
		{http.MethodGet, "/payments/", false},
	},
	"booking": {
		{http.MethodPost, "/booking", false},
		{http.MethodGet, "/bookings/", false},
		{http.MethodOptions, "/bookings", true},
	},
	"users": {
		{http.MethodGet, "/users/", false},
	},
	"orders": {
		{http.MethodPut, "/order/", false},
	},
	"foods": {
		{http.MethodPost, "/foods/data", true},
	},
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !knownPath(h.service, r.URL.Path) {
		h.reject(w, r.URL.Path, http.StatusNotFound, "route not found")
		return
	}
	if !matchEndpoint(h.service, r.Method, r.URL.Path) {
		h.reject(w, r.URL.Path, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()

	if rand.Float64() < h.cfg.ErrorRate(time.Now()) {
		h.fail(w, r.Method, r.URL.Path)
		return
	}
	h.log.Info(h.service, "accepted "+r.Method+" "+r.URL.Path, string(body))
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(successBody(r.Method, r.URL.Path))
}

func knownPath(service, path string) bool {
	for _, ep := range serviceEndpoints[service] {
		if pathMatches(ep, path) {
			return true
		}
	}
	return false
}

func matchEndpoint(service, method, path string) bool {
	for _, ep := range serviceEndpoints[service] {
		if ep.method == method && pathMatches(ep, path) {
			return true
		}
	}
	return false
}

func pathMatches(ep endpoint, path string) bool {
	if ep.exact {
		return path == ep.path
	}
	// "/booking" must not also match "/bookings/...".
	if strings.HasSuffix(ep.path, "/") {
		return strings.HasPrefix(path, ep.path)
	}
	return path == ep.path || strings.HasPrefix(path, ep.path+"/")
}

func successBody(method, path string) []byte {
	id := path[strings.LastIndex(path, "/")+1:]
	switch method {
	case http.MethodGet:
		return fmt.Appendf(nil, `{"id":"%s","status":"ok"}`, id)
	case http.MethodPut:
		return fmt.Appendf(nil, `{"id":"%s","status":"updated"}`, id)
	default:
		return []byte(`{"status":"ok"}`)
	}
}

func (h *handler) fail(w http.ResponseWriter, method, path string) {
	reason := processingReason(h.service)
	stack := strings.TrimRight(string(debug.Stack()), "\n")
	h.log.Error(h.service, method+" "+path+" failed reason="+reason, stack)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = fmt.Fprintf(w, "reason: %s\n%s", reason, stack)
}

func (h *handler) reject(w http.ResponseWriter, path string, status int, reason string) {
	h.log.Error(h.service, fmt.Sprintf("request failed %s reason=%s", path, reason), reason)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "reason: "+reason+"\n")
}

func processingReason(service string) string {
	choices := map[string][]string{
		"booking": {
			"inventory hold expired",
			"booking upstream timed out",
		},
		"users": {
			"user not found",
			"profile upstream timed out",
		},
		"orders": {
			"order update rejected",
			"order upstream timed out",
		},
		"foods": {
			"kitchen rejected the order",
			"foods upstream returned 503",
		},
	}
	list := choices[service]
	if list == nil {
		list = []string{
			"charge rejected by gateway",
			"upstream returned 503",
		}
	}
	return list[rand.Intn(len(list))]
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

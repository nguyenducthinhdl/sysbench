package metrics

import (
	"io"
	"log"
	"net/http"
	"sync"
)

// maxPushBody is the largest exposition body the dashboard receiver stores.
const maxPushBody = 8 << 20

// Receiver stores the latest client exposition and serves it for scraping.
// POST carries the scenario basic auth. GET /metrics does not: Prometheus
// on the docker network is not the client that the scenario authenticates.
type Receiver struct {
	Username string
	Password string
	Logger   *log.Logger

	mu   sync.Mutex
	body []byte
}

// Handler accepts the dashboard POST and serves GET /metrics and GET /health.
func (r *Receiver) Handler() http.Handler {
	return http.HandlerFunc(r.serve)
}

func (r *Receiver) serve(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		switch req.URL.Path {
		case "/health", "/":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok\n")
		case "/metrics":
			r.mu.Lock()
			body := append([]byte(nil), r.body...)
			r.mu.Unlock()
			w.Header().Set("Content-Type", ContentType)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		default:
			http.NotFound(w, req)
		}
	case http.MethodPost:
		if r.Username != "" {
			user, pass, ok := req.BasicAuth()
			if !ok || user != r.Username || pass != r.Password {
				w.Header().Set("WWW-Authenticate", `Basic realm="syscon-bench"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxPushBody)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "bad body", http.StatusRequestEntityTooLarge)
			return
		}
		r.mu.Lock()
		r.body = body
		r.mu.Unlock()
		if r.Logger != nil {
			r.Logger.Printf("dashboard stored %d bytes", len(body))
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// VictoriaLogs is the Phase 1 direct HTTP adapter.
//
// Push writes JSON lines to /insert/jsonline. message and time become _msg
// and _time. service, component, and log-severity are the stream fields.
// Search is a LogsQL stats query: a phrase on the message, exact equality
// on service / trace-id / uuid / status-code, or the same equality limited
// to the half-open window. Basic auth is sent when the scenario sets it.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	_ Sink    = (*VictoriaLogs)(nil)
	_ Querier = (*VictoriaLogs)(nil)
)

// VictoriaLogs posts JSON lines and runs /select/logsql/stats_query.
type VictoriaLogs struct {
	endpoint string
	user     string
	pass     string
	client   *http.Client
}

// NewVictoriaLogs builds a client for an absolute http or https URL.
// user is empty when the scenario auth type is none.
func NewVictoriaLogs(endpoint, user, pass string) (*VictoriaLogs, error) {
	if err := checkEndpoint("victorialogs", endpoint); err != nil {
		return nil, err
	}
	return &VictoriaLogs{
		endpoint: endpoint,
		user:     user,
		pass:     pass,
		client:   newClient(60 * time.Second),
	}, nil
}

// Push sends batch as JSON lines. An empty batch is a no-op.
func (v *VictoriaLogs) Push(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	body, err := victoriaBody(batch)
	if err != nil {
		return fmt.Errorf("victorialogs: encode: %w", err)
	}
	u, err := endpointURL(v.endpoint, "/insert/jsonline")
	if err != nil {
		return fmt.Errorf("victorialogs: endpoint: %w", err)
	}
	q := u.Query()
	q.Set("_msg_field", "message")
	q.Set("_time_field", "time")
	q.Set("_stream_fields", "service,component,log-severity")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("victorialogs: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/stream+json")
	setAuth(req, v.user, v.pass)
	status, respBody, err := call(v.client, req, false)
	if err != nil {
		return fmt.Errorf("victorialogs: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("victorialogs", status, respBody)
	}
	return nil
}

// Search runs one stats query and reads the body to the end.
// Hits is the summed vector. A non-2xx status is a *StatusError.
func (v *VictoriaLogs) Search(ctx context.Context, q Query) (Result, error) {
	form, err := victoriaQuery(q)
	if err != nil {
		return Result{}, err
	}
	u, err := endpointURL(v.endpoint, "/select/logsql/stats_query")
	if err != nil {
		return Result{}, fmt.Errorf("victorialogs: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, fmt.Errorf("victorialogs: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setAuth(req, v.user, v.pass)
	status, body, err := call(v.client, req, true)
	if err != nil {
		return Result{}, fmt.Errorf("victorialogs: %w", err)
	}
	if !okStatus(status) {
		return Result{Body: body}, fmt.Errorf("victorialogs: %w", &StatusError{Status: status})
	}
	hits, err := promVectorHits(body)
	if err != nil {
		return Result{Body: body}, fmt.Errorf("victorialogs: hits: %w", err)
	}
	return Result{Hits: hits, Body: body}, nil
}

func victoriaBody(batch []Event) ([]byte, error) {
	var buf bytes.Buffer
	for _, ev := range batch {
		if ev.BinaryField == nil {
			ev.BinaryField = []byte{}
		}
		line, err := json.Marshal(ev)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func victoriaQuery(q Query) (url.Values, error) {
	form := url.Values{}
	var filter string
	switch q.Class {
	case QueryFullText:
		if q.Token == "" {
			return nil, fmt.Errorf("victorialogs: full-text token is empty")
		}
		filter = quoteLiteral(q.Token)
	case QueryExact, QueryFilter:
		if q.Value == "" {
			return nil, fmt.Errorf("victorialogs: %s value is empty", q.Class)
		}
		switch q.Class {
		case QueryExact:
			switch q.Field {
			case "service", "trace-id", "uuid", "status-code":
			default:
				return nil, fmt.Errorf("victorialogs: unsupported field %q", q.Field)
			}
		case QueryFilter:
			switch q.Field {
			case "service", "log-severity", "status-code":
			default:
				return nil, fmt.Errorf("victorialogs: unsupported field %q", q.Field)
			}
			if q.From.IsZero() || !q.From.Before(q.To) {
				return nil, fmt.Errorf("victorialogs: filter window is empty")
			}
			form.Set("start", q.From.UTC().Format(time.RFC3339Nano))
			form.Set("end", q.To.UTC().Format(time.RFC3339Nano))
		}
		filter = quoteLiteral(q.Field) + ":=" + quoteLiteral(q.Value)
	default:
		return nil, fmt.Errorf("victorialogs: unknown query class %q", q.Class)
	}
	form.Set("query", filter+" | stats count() as hits")
	return form, nil
}

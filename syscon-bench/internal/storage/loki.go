// Loki is the Phase 1 direct HTTP adapter.
//
// Push sends one JSON /loki/api/v1/push. Indexed labels are job, service,
// severity, and component. The log line is the message, so a full-text
// search is Loki's substring filter. Higher-cardinality fields are
// structured metadata and are filtered by equality. A filter query counts
// over the half-open window; exact and full-text count over the last 24h,
// which covers the checked-in scenarios. Basic auth is sent when the
// scenario sets it.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const lokiJob = "syscon-bench"

var (
	_ Sink    = (*Loki)(nil)
	_ Querier = (*Loki)(nil)
)

// Loki posts log streams and runs instant metric queries.
type Loki struct {
	endpoint string
	user     string
	pass     string
	client   *http.Client
}

type lokiKey struct {
	service   string
	severity  string
	component string
}

// NewLoki builds a client for an absolute http or https URL.
// user is empty when the scenario auth type is none.
func NewLoki(endpoint, user, pass string) (*Loki, error) {
	if err := checkEndpoint("loki", endpoint); err != nil {
		return nil, err
	}
	return &Loki{
		endpoint: endpoint,
		user:     user,
		pass:     pass,
		client:   newClient(60 * time.Second),
	}, nil
}

// Push sends batch as one or more streams. An empty batch is a no-op.
func (l *Loki) Push(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	body, err := lokiBody(batch)
	if err != nil {
		return fmt.Errorf("loki: encode: %w", err)
	}
	u, err := endpointURL(l.endpoint, "/loki/api/v1/push")
	if err != nil {
		return fmt.Errorf("loki: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("loki: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req, l.user, l.pass)
	status, respBody, err := call(l.client, req, false)
	if err != nil {
		return fmt.Errorf("loki: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("loki", status, respBody)
	}
	return nil
}

// Search runs an instant count_over_time query and reads the body to the end.
// Hits is the summed vector. A non-2xx status is a *StatusError.
func (l *Loki) Search(ctx context.Context, q Query) (Result, error) {
	logql, eval, err := lokiQuery(q)
	if err != nil {
		return Result{}, err
	}
	u, err := endpointURL(l.endpoint, "/loki/api/v1/query")
	if err != nil {
		return Result{}, fmt.Errorf("loki: endpoint: %w", err)
	}
	vals := u.Query()
	vals.Set("query", logql)
	if eval != "" {
		vals.Set("time", eval)
	}
	u.RawQuery = vals.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("loki: request: %w", err)
	}
	setAuth(req, l.user, l.pass)
	status, body, err := call(l.client, req, true)
	if err != nil {
		return Result{}, fmt.Errorf("loki: %w", err)
	}
	if !okStatus(status) {
		return Result{Body: body}, fmt.Errorf("loki: %w", &StatusError{Status: status})
	}
	hits, err := promVectorHits(body)
	if err != nil {
		return Result{Body: body}, fmt.Errorf("loki: hits: %w", err)
	}
	return Result{Hits: hits, Body: body}, nil
}

func lokiBody(batch []Event) ([]byte, error) {
	type line struct {
		ns   string
		text string
		meta map[string]string
	}
	groups := map[lokiKey][]line{}
	for _, ev := range batch {
		key := lokiKey{service: ev.Service, severity: ev.LogSeverity, component: ev.Component}
		if key.service == "" {
			key.service = "unknown"
		}
		if key.severity == "" {
			key.severity = "info"
		}
		if key.component == "" {
			key.component = "unknown"
		}
		meta := map[string]string{}
		put := func(name, value string) {
			if value != "" {
				meta[name] = value
			}
		}
		put("trace_id", ev.TraceID)
		put("uuid", ev.UUID)
		put("status_code", strconv.Itoa(ev.StatusCode))
		put("instance_id", ev.InstanceID)
		put("host_name", ev.HostName)
		put("ip_address", ev.IPAddress)
		put("version", ev.Version)
		put("jira_id", ev.JiraID)
		put("group_id", ev.GroupID)
		groups[key] = append(groups[key], line{ns: lokiNanos(ev.Time), text: ev.Message, meta: meta})
	}
	type stream struct {
		Stream map[string]string `json:"stream"`
		Values [][]any           `json:"values"`
	}
	payload := struct {
		Streams []stream `json:"streams"`
	}{Streams: make([]stream, 0, len(groups))}
	keys := make([]lokiKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].service != keys[j].service {
			return keys[i].service < keys[j].service
		}
		if keys[i].severity != keys[j].severity {
			return keys[i].severity < keys[j].severity
		}
		return keys[i].component < keys[j].component
	})
	for _, key := range keys {
		lines := groups[key]
		sort.Slice(lines, func(i, j int) bool { return lines[i].ns < lines[j].ns })
		values := make([][]any, len(lines))
		for i, line := range lines {
			values[i] = []any{line.ns, line.text, line.meta}
		}
		payload.Streams = append(payload.Streams, stream{
			Stream: map[string]string{
				"job":       lokiJob,
				"service":   key.service,
				"severity":  key.severity,
				"component": key.component,
			},
			Values: values,
		})
	}
	return json.Marshal(payload)
}

func lokiNanos(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t = time.Now().UTC()
	}
	return strconv.FormatInt(t.UTC().UnixNano(), 10)
}

func lokiQuery(q Query) (string, string, error) {
	var b strings.Builder
	b.WriteString(`{job="`)
	b.WriteString(lokiJob)
	b.WriteByte('"')
	window := "24h"
	eval := ""
	switch q.Class {
	case QueryFullText:
		if q.Token == "" {
			return "", "", fmt.Errorf("loki: full-text token is empty")
		}
		b.WriteString(`} |= `)
		b.WriteString(quoteLiteral(q.Token))
	case QueryExact:
		if q.Value == "" {
			return "", "", fmt.Errorf("loki: exact value is empty")
		}
		switch q.Field {
		case "service":
			b.WriteString(`,service=`)
			b.WriteString(quoteLiteral(q.Value))
			b.WriteByte('}')
		case "trace-id":
			b.WriteString(`} | trace_id=`)
			b.WriteString(quoteLiteral(q.Value))
		case "uuid":
			b.WriteString(`} | uuid=`)
			b.WriteString(quoteLiteral(q.Value))
		case "status-code":
			b.WriteString(`} | status_code=`)
			b.WriteString(quoteLiteral(q.Value))
		default:
			return "", "", fmt.Errorf("loki: unsupported field %q", q.Field)
		}
	case QueryFilter:
		if q.Value == "" {
			return "", "", fmt.Errorf("loki: filter value is empty")
		}
		if q.From.IsZero() || !q.From.Before(q.To) {
			return "", "", fmt.Errorf("loki: filter window is empty")
		}
		switch q.Field {
		case "service":
			b.WriteString(`,service=`)
			b.WriteString(quoteLiteral(q.Value))
			b.WriteByte('}')
		case "log-severity":
			b.WriteString(`,severity=`)
			b.WriteString(quoteLiteral(q.Value))
			b.WriteByte('}')
		case "status-code":
			b.WriteString(`} | status_code=`)
			b.WriteString(quoteLiteral(q.Value))
		default:
			return "", "", fmt.Errorf("loki: unsupported field %q", q.Field)
		}
		secs := int(q.To.Sub(q.From).Seconds() + 0.999)
		if secs < 1 {
			secs = 1
		}
		window = fmt.Sprintf("%ds", secs)
		eval = q.To.UTC().Format(time.RFC3339Nano)
	default:
		return "", "", fmt.Errorf("loki: unknown query class %q", q.Class)
	}
	logql := fmt.Sprintf("sum(count_over_time(%s | keep job [%s]))", b.String(), window)
	return logql, eval, nil
}

// Elasticsearch is the Phase 1 direct HTTP adapter.
//
// Push writes a logsdb data stream through _bulk. Each document keeps the
// canonical fields and also sets @timestamp and host.name, which is the
// logsdb sort (host.name, then @timestamp). Search is _search with size 0:
// a match on message, a term on service / trace-id / uuid / status-code,
// or a range on @timestamp plus one term. Basic auth is sent when the
// scenario sets it. The index template is installed on the first call.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// esTemplate is applied to the data stream named logs.
// The analyzer splits on non-alphanumeric characters and keeps case, which
// is the Elasticsearch form of a token search. match_only_text uses the
// index default analyzer.
const esTemplate = `{
  "index_patterns": ["logs"],
  "data_stream": {},
  "priority": 500,
  "template": {
    "settings": {
      "index.mode": "logsdb",
      "index.number_of_shards": 1,
      "index.number_of_replicas": 0,
      "index.refresh_interval": "1s",
      "index.sort.field": ["host.name", "@timestamp"],
      "index.sort.order": ["asc", "desc"],
      "index.mapping.ignore_malformed": true,
      "analysis": {
        "analyzer": {
          "default": {"type": "custom", "tokenizer": "sysbench_split"}
        },
        "tokenizer": {
          "sysbench_split": {"type": "pattern", "pattern": "[^0-9A-Za-z]+"}
        }
      }
    },
    "mappings": {
      "properties": {
        "@timestamp": {"type": "date"},
        "message": {"type": "match_only_text"},
        "host": {"properties": {"name": {"type": "keyword"}}},
        "uuid": {"type": "keyword"},
        "instance-id": {"type": "keyword"},
        "ip-address": {"type": "keyword"},
        "host-name": {"type": "keyword"},
        "log-severity": {"type": "keyword"},
        "status-code": {"type": "integer"},
        "component": {"type": "keyword"},
        "trace-id": {"type": "keyword"},
        "version": {"type": "keyword"},
        "service": {"type": "keyword"},
        "time": {"type": "date"},
        "jira-id": {"type": "keyword"},
        "group-id": {"type": "keyword"},
        "external-01": {"type": "keyword"},
        "external-02": {"type": "keyword"},
        "external-03": {"type": "keyword"},
        "binary-field": {"type": "binary"}
      }
    }
  }
}`

var (
	_ Sink    = (*Elasticsearch)(nil)
	_ Querier = (*Elasticsearch)(nil)
)

// Elasticsearch posts _bulk batches and _search requests.
type Elasticsearch struct {
	endpoint string
	user     string
	pass     string
	client   *http.Client

	mu    sync.Mutex
	ready bool
}

type esHost struct {
	Name string `json:"name"`
}

type esDocument struct {
	Event
	At   string `json:"@timestamp"`
	Host esHost `json:"host"`
}

// NewElasticsearch builds a client for an absolute http or https URL.
// user is empty when the scenario auth type is none.
func NewElasticsearch(endpoint, user, pass string) (*Elasticsearch, error) {
	if err := checkEndpoint("elasticsearch", endpoint); err != nil {
		return nil, err
	}
	return &Elasticsearch{
		endpoint: endpoint,
		user:     user,
		pass:     pass,
		client:   newClient(60 * time.Second),
	}, nil
}

// Push indexes batch with the create action. An empty batch is a no-op.
// A bulk response with item errors fails the batch.
func (e *Elasticsearch) Push(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	if err := e.ensure(ctx); err != nil {
		return err
	}
	body, err := elasticsearchBody(batch)
	if err != nil {
		return fmt.Errorf("elasticsearch: encode: %w", err)
	}
	u, err := endpointURL(e.endpoint, "/logs/_bulk")
	if err != nil {
		return fmt.Errorf("elasticsearch: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("elasticsearch: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	setAuth(req, e.user, e.pass)
	// The bulk response is one JSON object with an item per event. The short
	// error buffer truncates it, so the success body has to be kept whole.
	status, respBody, err := call(e.client, req, true)
	if err != nil {
		return fmt.Errorf("elasticsearch: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("elasticsearch", status, respBody)
	}
	if err := elasticsearchBulkError(respBody); err != nil {
		return err
	}
	return nil
}

// Search runs one _search and reads the body to the end.
// Hits is the track_total_hits value. A non-2xx status is a *StatusError.
func (e *Elasticsearch) Search(ctx context.Context, q Query) (Result, error) {
	if err := e.ensure(ctx); err != nil {
		return Result{}, err
	}
	query, err := elasticsearchQuery(q)
	if err != nil {
		return Result{}, err
	}
	bodyDoc := map[string]any{
		"size":             0,
		"track_total_hits": true,
		"query":            query,
	}
	if q.Class == QueryAggregation {
		bodyDoc["aggs"] = map[string]any{
			"message_bytes": map[string]any{
				"sum": map[string]any{
					"script": map[string]any{"source": "params._source.message.length()"},
				},
			},
		}
	}
	payload, err := json.Marshal(bodyDoc)
	if err != nil {
		return Result{}, fmt.Errorf("elasticsearch: encode: %w", err)
	}
	u, err := endpointURL(e.endpoint, "/logs/_search")
	if err != nil {
		return Result{}, fmt.Errorf("elasticsearch: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("elasticsearch: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req, e.user, e.pass)
	status, body, err := call(e.client, req, true)
	if err != nil {
		return Result{}, fmt.Errorf("elasticsearch: %w", err)
	}
	if !okStatus(status) {
		return Result{Body: body}, fmt.Errorf("elasticsearch: %w", &StatusError{Status: status})
	}
	hits, err := elasticsearchHits(body)
	if err != nil {
		return Result{Body: body}, fmt.Errorf("elasticsearch: hits: %w", err)
	}
	return Result{Hits: hits, Body: body}, nil
}

func (e *Elasticsearch) ensure(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ready {
		return nil
	}
	u, err := endpointURL(e.endpoint, "/_index_template/syscon-logs")
	if err != nil {
		return fmt.Errorf("elasticsearch: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), strings.NewReader(esTemplate))
	if err != nil {
		return fmt.Errorf("elasticsearch: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setAuth(req, e.user, e.pass)
	status, body, err := call(e.client, req, false)
	if err != nil {
		return fmt.Errorf("elasticsearch: template: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("elasticsearch", status, body)
	}
	e.ready = true
	return nil
}

func elasticsearchBody(batch []Event) ([]byte, error) {
	var buf bytes.Buffer
	for _, ev := range batch {
		if ev.BinaryField == nil {
			ev.BinaryField = []byte{}
		}
		meta := map[string]string{}
		if ev.UUID != "" {
			meta["_id"] = ev.UUID
		}
		action, err := json.Marshal(map[string]any{"create": meta})
		if err != nil {
			return nil, err
		}
		doc, err := json.Marshal(esDocument{
			Event: ev,
			At:    ev.Time,
			Host:  esHost{Name: ev.HostName},
		})
		if err != nil {
			return nil, err
		}
		buf.Write(action)
		buf.WriteByte('\n')
		buf.Write(doc)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func elasticsearchBulkError(body []byte) error {
	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("elasticsearch: bulk: %w", err)
	}
	if !parsed.Errors {
		return nil
	}
	for _, item := range parsed.Items {
		for _, op := range item {
			if op.Status >= 300 {
				msg := strings.TrimSpace(string(op.Error))
				if len(msg) > 512 {
					msg = msg[:512]
				}
				return fmt.Errorf("elasticsearch: bulk status %d: %s", op.Status, msg)
			}
		}
	}
	return fmt.Errorf("elasticsearch: bulk reported errors")
}

func elasticsearchQuery(q Query) (any, error) {
	switch q.Class {
	case QueryFullText:
		if q.Token == "" {
			return nil, fmt.Errorf("elasticsearch: full-text token is empty")
		}
		// term looks up one analyzed token. match would score by term frequency,
		// and this field does not store term frequency.
		return map[string]any{"term": map[string]any{"message": q.Token}}, nil
	case QueryExact:
		value, err := elasticsearchValue(q.Class, q.Field, q.Value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"term": map[string]any{q.Field: value}}, nil
	case QueryFilter:
		if q.From.IsZero() || !q.From.Before(q.To) {
			return nil, fmt.Errorf("elasticsearch: filter window is empty")
		}
		value, err := elasticsearchValue(q.Class, q.Field, q.Value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"range": map[string]any{"@timestamp": map[string]string{
				"gte": q.From.UTC().Format(time.RFC3339Nano),
				"lt":  q.To.UTC().Format(time.RFC3339Nano),
			}}},
			map[string]any{"term": map[string]any{q.Field: value}},
		}}}, nil
	case QueryAggregation:
		if q.From.IsZero() || !q.From.Before(q.To) {
			return nil, fmt.Errorf("elasticsearch: aggregation window is empty")
		}
		return map[string]any{"range": map[string]any{"@timestamp": map[string]string{
			"gte": q.From.UTC().Format(time.RFC3339Nano),
			"lt":  q.To.UTC().Format(time.RFC3339Nano),
		}}}, nil
	default:
		return nil, fmt.Errorf("elasticsearch: unknown query class %q", q.Class)
	}
}

func elasticsearchValue(class QueryClass, field, value string) (any, error) {
	switch class {
	case QueryExact:
		switch field {
		case "service", "trace-id", "uuid":
		case "status-code":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("elasticsearch: status-code %q", value)
			}
			return n, nil
		default:
			return nil, fmt.Errorf("elasticsearch: unsupported field %q", field)
		}
	case QueryFilter:
		switch field {
		case "service", "log-severity":
		case "status-code":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("elasticsearch: status-code %q", value)
			}
			return n, nil
		default:
			return nil, fmt.Errorf("elasticsearch: unsupported field %q", field)
		}
	default:
		return nil, fmt.Errorf("elasticsearch: unsupported field %q", field)
	}
	if value == "" {
		return nil, fmt.Errorf("elasticsearch: %s value is empty", field)
	}
	return value, nil
}

func elasticsearchHits(body []byte) (int, error) {
	var parsed struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
		} `json:"hits"`
		Aggregations map[string]struct {
			Value float64 `json:"value"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if agg, ok := parsed.Aggregations["message_bytes"]; ok {
		return int(agg.Value), nil
	}
	return parsed.Hits.Total.Value, nil
}

// Stored is the number of documents in the logs data stream.
// A missing data stream is zero.
func (e *Elasticsearch) Stored(ctx context.Context) (int64, error) {
	if err := e.ensure(ctx); err != nil {
		return 0, err
	}
	u, err := endpointURL(e.endpoint, "/logs/_count")
	if err != nil {
		return 0, fmt.Errorf("elasticsearch: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("elasticsearch: request: %w", err)
	}
	setAuth(req, e.user, e.pass)
	status, body, err := call(e.client, req, false)
	if err != nil {
		return 0, fmt.Errorf("elasticsearch: %w", err)
	}
	if missingStore(status, body) {
		return 0, nil
	}
	if !okStatus(status) {
		return 0, httpStatusError("elasticsearch", status, body)
	}
	var parsed struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("elasticsearch: count: %w", err)
	}
	return parsed.Count, nil
}

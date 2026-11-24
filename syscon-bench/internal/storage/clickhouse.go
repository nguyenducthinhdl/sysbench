// ClickHouse is the Phase 1 direct HTTP adapter.
//
// Push inserts JSONEachRow into syscon.logs. Search counts matches:
// hasToken on message, equality on service / trace-id / uuid / status-code,
// and a half-open time window plus one equality. Basic auth is sent when
// the scenario sets it. The table is created on the first call.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	clickHouseDatabase = "CREATE DATABASE IF NOT EXISTS syscon"
	clickHouseInsert   = "INSERT INTO syscon.logs SETTINGS date_time_input_format = 'best_effort' FORMAT JSONEachRow"
	// clickHouseTable is the direct-insert target. The text index matches
	// hasToken: tokens are split on non-alphanumeric characters.
	// The VM install script creates this same table and may withhold DDL
	// from the scenario user; ensure treats a readable table as ready.
	clickHouseTable = "" +
		"CREATE TABLE IF NOT EXISTS syscon.logs (\n" +
		"    message String,\n" +
		"    uuid String,\n" +
		"    `instance-id` String,\n" +
		"    `ip-address` String,\n" +
		"    `host-name` LowCardinality(String),\n" +
		"    `log-severity` LowCardinality(String),\n" +
		"    `status-code` UInt16,\n" +
		"    component LowCardinality(String),\n" +
		"    `trace-id` String,\n" +
		"    version LowCardinality(String),\n" +
		"    service LowCardinality(String),\n" +
		"    time DateTime64(3, 'UTC'),\n" +
		"    `jira-id` String,\n" +
		"    `group-id` LowCardinality(String),\n" +
		"    `external-01` LowCardinality(String),\n" +
		"    `external-02` LowCardinality(String),\n" +
		"    `external-03` LowCardinality(String),\n" +
		"    `binary-field` String,\n" +
		"    INDEX idx_message message TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 64\n" +
		") ENGINE = MergeTree\n" +
		"PARTITION BY toDate(time)\n" +
		"ORDER BY (service, time, uuid)\n"
)

var (
	_ Sink    = (*ClickHouse)(nil)
	_ Querier = (*ClickHouse)(nil)
)

// ClickHouse posts batches and searches to the HTTP interface.
type ClickHouse struct {
	endpoint string
	user     string
	pass     string
	client   *http.Client

	mu    sync.Mutex
	ready bool
}

// NewClickHouse builds a client for an absolute http or https URL.
// user is empty when the scenario auth type is none.
func NewClickHouse(endpoint, user, pass string) (*ClickHouse, error) {
	if err := checkEndpoint("clickhouse", endpoint); err != nil {
		return nil, err
	}
	return &ClickHouse{
		endpoint: endpoint,
		user:     user,
		pass:     pass,
		client:   newClient(60 * time.Second),
	}, nil
}

// Push inserts batch as JSONEachRow. An empty batch is a no-op.
// Non-2xx, timeout, and transport errors fail the batch.
func (c *ClickHouse) Push(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	if err := c.ensure(ctx); err != nil {
		return err
	}
	body, err := clickHouseBody(batch)
	if err != nil {
		return fmt.Errorf("clickhouse: encode: %w", err)
	}
	u, err := endpointURL(c.endpoint, "/")
	if err != nil {
		return fmt.Errorf("clickhouse: endpoint: %w", err)
	}
	q := u.Query()
	q.Set("query", clickHouseInsert)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("clickhouse: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	c.auth(req)
	status, respBody, err := call(c.client, req, false)
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("clickhouse", status, respBody)
	}
	return nil
}

// Search runs the class as a count() query and reads the body to the end.
// A non-2xx status is a *StatusError after the body has been read.
func (c *ClickHouse) Search(ctx context.Context, q Query) (Result, error) {
	if err := c.ensure(ctx); err != nil {
		return Result{}, err
	}
	sql, params, err := clickHouseSQL(q)
	if err != nil {
		return Result{}, err
	}
	u, err := endpointURL(c.endpoint, "/")
	if err != nil {
		return Result{}, fmt.Errorf("clickhouse: endpoint: %w", err)
	}
	vals := u.Query()
	for key, value := range params {
		vals.Set(key, value)
	}
	u.RawQuery = vals.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader([]byte(sql)))
	if err != nil {
		return Result{}, fmt.Errorf("clickhouse: request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	c.auth(req)
	status, body, err := call(c.client, req, true)
	if err != nil {
		return Result{}, fmt.Errorf("clickhouse: %w", err)
	}
	if !okStatus(status) {
		return Result{Body: body}, fmt.Errorf("clickhouse: %w", &StatusError{Status: status})
	}
	hits, err := clickHouseHits(body)
	if err != nil {
		return Result{Body: body}, fmt.Errorf("clickhouse: hits: %w", err)
	}
	return Result{Hits: hits, Body: body}, nil
}

func (c *ClickHouse) auth(req *http.Request) {
	// ClickHouse 26.2 rejects a request that carries both basic auth and
	// X-ClickHouse-User / X-ClickHouse-Key. The scenario auth is basic.
	setAuth(req, c.user, c.pass)
}

func (c *ClickHouse) ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	dbErr := c.exec(ctx, clickHouseDatabase)
	tblErr := c.exec(ctx, clickHouseTable)
	if dbErr != nil || tblErr != nil {
		// The VM user may have INSERT and SELECT only. A readable table is enough.
		if err := c.exec(ctx, "SELECT 1 FROM syscon.logs LIMIT 0"); err != nil {
			if tblErr != nil {
				return tblErr
			}
			return dbErr
		}
	}
	c.ready = true
	return nil
}

func (c *ClickHouse) exec(ctx context.Context, sql string) error {
	u, err := endpointURL(c.endpoint, "/")
	if err != nil {
		return fmt.Errorf("clickhouse: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader([]byte(sql)))
	if err != nil {
		return fmt.Errorf("clickhouse: request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	c.auth(req)
	status, body, err := call(c.client, req, false)
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("clickhouse", status, body)
	}
	return nil
}

func clickHouseBody(batch []Event) ([]byte, error) {
	var buf bytes.Buffer
	for _, ev := range batch {
		ev.Time = clickHouseTime(ev.Time)
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

func clickHouseTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("2006-01-02 15:04:05.000")
}

func clickHouseSQL(q Query) (string, map[string]string, error) {
	params := map[string]string{}
	switch q.Class {
	case QueryFullText:
		if q.Token == "" {
			return "", nil, fmt.Errorf("clickhouse: full-text token is empty")
		}
		params["param_tok"] = q.Token
		return "SELECT count() AS hits FROM syscon.logs WHERE hasToken(message, {tok:String}) FORMAT JSON", params, nil
	case QueryExact:
		col, err := clickHouseColumn(q.Class, q.Field)
		if err != nil {
			return "", nil, err
		}
		if q.Value == "" {
			return "", nil, fmt.Errorf("clickhouse: exact value is empty")
		}
		params["param_val"] = q.Value
		return fmt.Sprintf("SELECT count() AS hits FROM syscon.logs WHERE toString(%s) = {val:String} FORMAT JSON", col), params, nil
	case QueryFilter:
		if q.From.IsZero() || !q.From.Before(q.To) {
			return "", nil, fmt.Errorf("clickhouse: filter window is empty")
		}
		svc := q.Service
		if svc == "" && q.Field == "service" {
			svc = q.Value
		}
		if svc == "" {
			return "", nil, fmt.Errorf("clickhouse: filter service is empty")
		}
		params["param_svc"] = svc
		params["param_t_from"] = q.From.UTC().Format("2006-01-02 15:04:05.000")
		params["param_t_to"] = q.To.UTC().Format("2006-01-02 15:04:05.000")
		sql := "SELECT count() AS hits FROM syscon.logs WHERE service = {svc:String} AND time >= {t_from:DateTime64(3)} AND time < {t_to:DateTime64(3)} FORMAT JSON"
		return sql, params, nil
	case QueryAggregation:
		if q.From.IsZero() || !q.From.Before(q.To) {
			return "", nil, fmt.Errorf("clickhouse: aggregation window is empty")
		}
		params["param_t_from"] = q.From.UTC().Format("2006-01-02 15:04:05.000")
		params["param_t_to"] = q.To.UTC().Format("2006-01-02 15:04:05.000")
		sql := "SELECT sum(length(message)) AS hits FROM syscon.logs WHERE time >= {t_from:DateTime64(3)} AND time < {t_to:DateTime64(3)} FORMAT JSON"
		return sql, params, nil
	default:
		return "", nil, fmt.Errorf("clickhouse: unknown query class %q", q.Class)
	}
}

func clickHouseColumn(class QueryClass, field string) (string, error) {
	switch class {
	case QueryExact:
		switch field {
		case "service", "trace-id", "uuid", "status-code":
			return "`" + field + "`", nil
		}
	case QueryFilter:
		switch field {
		case "service", "log-severity", "status-code":
			return "`" + field + "`", nil
		}
	}
	return "", fmt.Errorf("clickhouse: unsupported field %q", field)
}

func clickHouseHits(body []byte) (int, error) {
	var parsed struct {
		Data []struct {
			Hits any `json:"hits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if len(parsed.Data) == 0 {
		return 0, nil
	}
	return asInt(parsed.Data[0].Hits)
}

// Stored is the number of rows in syscon.logs. A missing table is zero.
func (c *ClickHouse) Stored(ctx context.Context) (int64, error) {
	if err := c.ensure(ctx); err != nil {
		return 0, err
	}
	u, err := endpointURL(c.endpoint, "/")
	if err != nil {
		return 0, fmt.Errorf("clickhouse: endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader("SELECT count() FROM syscon.logs FORMAT TabSeparated"))
	if err != nil {
		return 0, fmt.Errorf("clickhouse: request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	c.auth(req)
	status, body, err := call(c.client, req, false)
	if err != nil {
		return 0, fmt.Errorf("clickhouse: %w", err)
	}
	if missingStore(status, body) {
		return 0, nil
	}
	if !okStatus(status) {
		return 0, httpStatusError("clickhouse", status, body)
	}
	n, err := parseInt64Body(body)
	if err != nil {
		return 0, fmt.Errorf("clickhouse: count: %w", err)
	}
	return n, nil
}

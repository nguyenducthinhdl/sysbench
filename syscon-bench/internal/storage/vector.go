// Vector is the Phase 2 ingest sink.
//
// Push posts one JSON object per line. The sidecar accepts that body and
// forwards it to storage. This client does not search. The engine adapter
// stays the Querier, because a query through the agent is not a storage query.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Vector posts newline-delimited events to the local sidecar.
type Vector struct {
	endpoint string
	client   *http.Client
}

// NewVector builds a client for an absolute http or https URL.
// The sidecar does not use the scenario basic auth.
func NewVector(endpoint string) (*Vector, error) {
	if err := checkEndpoint("vector", endpoint); err != nil {
		return nil, err
	}
	return &Vector{
		endpoint: endpoint,
		client:   newClient(60 * time.Second),
	}, nil
}

// Push encodes batch as newline-delimited JSON and POSTs it.
// An empty batch is a no-op. A 2xx response means Vector accepted the body,
// which can be earlier than the storage flush.
func (v *Vector) Push(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, ev := range batch {
		line, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("vector: encode: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("vector: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	setAuth(req, "", "")
	status, body, err := call(v.client, req, false)
	if err != nil {
		return fmt.Errorf("vector: %w", err)
	}
	if !okStatus(status) {
		return httpStatusError("vector", status, body)
	}
	return nil
}

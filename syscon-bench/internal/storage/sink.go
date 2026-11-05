package storage

import (
	"context"
	"fmt"
)

// Sink pushes one batch. Implementations must be safe for concurrent Push.
// A non-nil error means the batch was not accepted. Callers do not retry.
type Sink interface {
	Push(ctx context.Context, batch []Event) error
}

// Querier runs one search. Search reads the response body to the end before
// returning, so the query client can time the call as latency.
// A non-2xx response whose body was fully read returns *StatusError; Result
// may still carry that body and a hit count. A timeout or transport error is
// a plain error and means the body was not read. Implementations must be safe
// for concurrent Search.
type Querier interface {
	Search(ctx context.Context, q Query) (Result, error)
}

// StatusError is a search response outside 2xx whose body was fully read.
// The query client records latency and a query error.
type StatusError struct {
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("status %d", e.Status)
}

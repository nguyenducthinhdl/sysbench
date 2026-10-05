package main

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func TestFailureReasonUsesBackendCause(t *testing.T) {
	body := "reason: charge rejected by gateway\ngoroutine 1 [running]:\nmain.fail()"
	if got := failureReason(http.StatusInternalServerError, body); got != "charge rejected by gateway" {
		t.Fatalf("got %q", got)
	}
}

func TestFailureReasonByStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusTooManyRequests, "local_rate_limited", "rate limit exceeded"},
		{http.StatusServiceUnavailable, "", "upstream unavailable"},
		{http.StatusServiceUnavailable, "upstream connect error or disconnect/reset before headers. reset reason: connection termination", "upstream connect error or disconnect/reset before headers. reset reason: connection termination"},
		{http.StatusGatewayTimeout, "", "upstream timeout"},
		{http.StatusNotFound, "reason: route not found\n", "route not found"},
		{http.StatusInternalServerError, "goroutine 1 [running]:\nmain.fail()", "backend processing failed"},
	}
	for _, tc := range cases {
		if got := failureReason(tc.status, tc.body); got != tc.want {
			t.Fatalf("status %d body %q: got %q", tc.status, tc.body, got)
		}
	}
}

func TestTransportReason(t *testing.T) {
	if got := transportReason(errors.New("dial tcp 127.0.0.1:10000: connect: connection refused")); got != "envoy unreachable" {
		t.Fatalf("refused: %q", got)
	}
	err := &url.Error{Op: "Post", URL: "http://envoy:10000/payments", Err: timeoutErr{}}
	if got := transportReason(err); got != "request timeout" {
		t.Fatalf("timeout: %q", got)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "context deadline exceeded" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

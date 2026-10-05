package main

import "testing"

func TestProcessingReasonStaysInSet(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		seen[processingReason("payments")] = true
		seen[processingReason("booking")] = true
	}
	for _, reason := range []string{
		"charge rejected by gateway",
		"upstream returned 503",
		"inventory hold expired",
		"booking upstream timed out",
	} {
		if !seen[reason] {
			t.Fatalf("missing %q in %v", reason, seen)
		}
	}
}

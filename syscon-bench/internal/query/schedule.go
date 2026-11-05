package query

import (
	"math"

	"syscon-bench/internal/scenario"
)

// Count is the number of query ticks in the segment.
// The integral is query_rate / 60 * window. A fractional integral rounds to
// the nearest tick, the same way the generator rounds offered events.
// The checked-in scenarios are already whole numbers. A rate of 0 is no ticks.
func Count(seg scenario.Segment) int {
	total := integral(seg)
	if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return 0
	}
	n := math.Round(total)
	if n <= 0 || n > float64(math.MaxInt) {
		return 0
	}
	return int(n)
}

// Due is the scenario time, in seconds from the run start, when tick i of seg
// is offered. i is 0-based and must be in [0, Count(seg)).
// Ticks sit on half-steps inside the half-open window, so none land on the
// segment end. The run does not start a query at duration.
func Due(seg scenario.Segment, i int) float64 {
	n := Count(seg)
	if n <= 0 || i < 0 || i >= n {
		return seg.Start
	}
	window := seg.TimeStamp - seg.Start
	u := (float64(i) + 0.5) * window / float64(n)
	if u < 0 {
		u = 0
	}
	if window > 0 && u >= window {
		u = math.Nextafter(window, 0)
	}
	return seg.Start + u
}

// Schedule is every tick in the scenario, in order.
// Each time is seconds from the run start and falls inside its segment.
func Schedule(sc *scenario.Scenario) []float64 {
	if sc == nil {
		return nil
	}
	n := 0
	for _, seg := range sc.Segments {
		n += Count(seg)
	}
	if n == 0 {
		return nil
	}
	out := make([]float64, 0, n)
	for _, seg := range sc.Segments {
		ticks := Count(seg)
		for i := 0; i < ticks; i++ {
			out = append(out, Due(seg, i))
		}
	}
	return out
}

func integral(seg scenario.Segment) float64 {
	window := seg.TimeStamp - seg.Start
	if window <= 0 || seg.QueryRate <= 0 {
		return 0
	}
	return seg.QueryRate / 60 * window
}

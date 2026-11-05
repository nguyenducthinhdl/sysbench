package generate

import (
	"math"

	"syscon-bench/internal/scenario"
)

// Offered is the number of events the segment schedules.
// The integral is (rps_min + rps_max) / 2 * window for linear, and
// rps_max * window for spike. A fractional integral rounds to the nearest
// event. The checked-in scenarios are already whole numbers.
func Offered(seg scenario.Segment) int {
	total := offeredExact(seg)
	if total <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return 0
	}
	n := math.Round(total)
	if n <= 0 || n > float64(math.MaxInt) {
		return 0
	}
	return int(n)
}

// TotalOffered sums Offered across the scenario.
func TotalOffered(sc *scenario.Scenario) int {
	if sc == nil {
		return 0
	}
	n := 0
	for _, seg := range sc.Segments {
		n += Offered(seg)
	}
	return n
}

// Queries is the expected query count for the segment: query_rate / 60 * window.
// The query client spaces that many ticks across the window.
func Queries(seg scenario.Segment) float64 {
	window := seg.TimeStamp - seg.Start
	if window <= 0 {
		return 0
	}
	return seg.QueryRate / 60 * window
}

// RateAt is the offered events per second at t seconds from the run start.
// A boundary belongs to the following segment. t outside [0, duration) is 0.
// Spike holds rps_max_concurrent for the whole window.
func RateAt(sc *scenario.Scenario, t float64) float64 {
	if sc == nil || t < 0 || t >= sc.Configs.Duration {
		return 0
	}
	for _, seg := range sc.Segments {
		if t >= seg.Start && t < seg.TimeStamp {
			return rateIn(seg, t)
		}
	}
	return 0
}

// EventDue is the scenario time, in seconds from the run start, when event i
// of seg is offered. i is 0-based and must be in [0, Offered(seg)).
// Events sit on the cumulative curve at half-steps, so every due time is
// inside the half-open window.
func EventDue(seg scenario.Segment, i int) float64 {
	n := Offered(seg)
	total := offeredExact(seg)
	if n <= 0 || total <= 0 || i < 0 || i >= n {
		return seg.Start
	}
	target := (float64(i) + 0.5) * total / float64(n)
	u := invertCumulative(seg, target)
	window := seg.TimeStamp - seg.Start
	if u < 0 {
		u = 0
	}
	if window > 0 && u >= window {
		u = math.Nextafter(window, 0)
	}
	return seg.Start + u
}

func offeredExact(seg scenario.Segment) float64 {
	window := seg.TimeStamp - seg.Start
	if window <= 0 {
		return 0
	}
	if seg.Type == "spike" {
		return seg.RPSMax * window
	}
	return (seg.RPSMin + seg.RPSMax) / 2 * window
}

func rateIn(seg scenario.Segment, t float64) float64 {
	if seg.Type == "spike" {
		return seg.RPSMax
	}
	window := seg.TimeStamp - seg.Start
	if window <= 0 {
		return seg.RPSMin
	}
	u := t - seg.Start
	return seg.RPSMin + (seg.RPSMax-seg.RPSMin)*(u/window)
}

// invertCumulative solves the offered-event integral for an offset inside the window.
// Linear rate rmin + (rmax-rmin) * u / window integrates to
// rmin*u + (rmax-rmin)*u^2 / (2*window).
func invertCumulative(seg scenario.Segment, target float64) float64 {
	window := seg.TimeStamp - seg.Start
	if window <= 0 || target <= 0 {
		return 0
	}
	if seg.Type == "spike" {
		if seg.RPSMax <= 0 {
			return 0
		}
		return target / seg.RPSMax
	}
	delta := seg.RPSMax - seg.RPSMin
	if math.Abs(delta) < 1e-9 {
		if seg.RPSMin <= 0 {
			return 0
		}
		return target / seg.RPSMin
	}
	a := delta / (2 * window)
	b := seg.RPSMin
	disc := b*b + 4*a*target
	if disc < 0 {
		disc = 0
	}
	return (-b + math.Sqrt(disc)) / (2 * a)
}

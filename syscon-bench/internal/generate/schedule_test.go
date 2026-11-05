package generate

import (
	"math"
	"testing"

	"syscon-bench/internal/scenario"
)

func TestCheckedInIntegrals(t *testing.T) {
	s1, err := scenario.Load("../../log-generator/scenarios/test-scenario-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if TotalOffered(s1) != 360000 {
		t.Fatalf("scenario 01 offered %d", TotalOffered(s1))
	}
	if math.Abs(Queries(s1.Segments[0])-60) > 1e-9 {
		t.Fatalf("scenario 01 queries %v", Queries(s1.Segments[0]))
	}

	s2, err := scenario.Load("../../log-generator/scenarios/test-scenario-02.yaml")
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []int{6000, 180000, 3120000, 2400000, 60000}
	wantQueries := []float64{1, 300, 2600, 2000, 1000}
	if len(s2.Segments) != len(wantEvents) {
		t.Fatalf("segments %d", len(s2.Segments))
	}
	sumQ := 0.0
	for i, seg := range s2.Segments {
		if Offered(seg) != wantEvents[i] {
			t.Fatalf("%s offered %d", seg.Name, Offered(seg))
		}
		if math.Abs(Queries(seg)-wantQueries[i]) > 1e-6 {
			t.Fatalf("%s queries %v", seg.Name, Queries(seg))
		}
		sumQ += Queries(seg)
	}
	if TotalOffered(s2) != 5766000 {
		t.Fatalf("scenario 02 offered %d", TotalOffered(s2))
	}
	if math.Abs(sumQ-5901) > 1e-6 {
		t.Fatalf("queries %v", sumQ)
	}

	if RateAt(s2, 0) != 100 || RateAt(s2, 60) != 1000 || RateAt(s2, 240) != 2000 {
		t.Fatalf("rates %v %v %v", RateAt(s2, 0), RateAt(s2, 60), RateAt(s2, 240))
	}
	if RateAt(s2, 1800) != 2000 || RateAt(s2, 2500) != 2000 || RateAt(s2, 3000) != 100 {
		t.Fatalf("later rates %v %v %v", RateAt(s2, 1800), RateAt(s2, 2500), RateAt(s2, 3000))
	}
	if RateAt(s2, 3600) != 0 || RateAt(s2, -1) != 0 {
		t.Fatal("outside the run")
	}
	// spike-03 holds rps_max even though rps_min is higher.
	if RateAt(s2, 3300) != 100 {
		t.Fatalf("spike-03 %v", RateAt(s2, 3300))
	}
}

func TestRateBoundaryBelongsToNextSegment(t *testing.T) {
	sc := &scenario.Scenario{
		Configs: scenario.Configs{Duration: 20},
		Segments: []scenario.Segment{
			{Name: "ramp", Start: 0, TimeStamp: 10, Type: "linear", RPSMin: 0, RPSMax: 100},
			{Name: "hold", Start: 10, TimeStamp: 20, Type: "spike", RPSMin: 999, RPSMax: 5},
		},
	}
	if RateAt(sc, 0) != 0 || RateAt(sc, 5) != 50 || RateAt(sc, 10) != 5 || RateAt(sc, 20) != 0 {
		t.Fatalf("%v %v %v %v", RateAt(sc, 0), RateAt(sc, 5), RateAt(sc, 10), RateAt(sc, 20))
	}
}

func TestEventDueInsideWindow(t *testing.T) {
	seg := scenario.Segment{Start: 0, TimeStamp: 1, Type: "linear", RPSMin: 100, RPSMax: 100}
	n := Offered(seg)
	if n != 100 {
		t.Fatalf("offered %d", n)
	}
	if math.Abs(EventDue(seg, 0)-0.005) > 1e-9 {
		t.Fatalf("first %v", EventDue(seg, 0))
	}
	if math.Abs(EventDue(seg, 99)-0.995) > 1e-9 {
		t.Fatalf("last %v", EventDue(seg, 99))
	}
	prev := -1.0
	for i := 0; i < n; i++ {
		due := EventDue(seg, i)
		if due < seg.Start || due >= seg.TimeStamp || due <= prev {
			t.Fatalf("due %d = %v", i, due)
		}
		prev = due
	}
}

func TestLinearGapsFollowTheSlope(t *testing.T) {
	up := scenario.Segment{Start: 0, TimeStamp: 10, Type: "linear", RPSMin: 0, RPSMax: 100}
	down := scenario.Segment{Start: 0, TimeStamp: 10, Type: "linear", RPSMin: 100, RPSMax: 0}
	if Offered(up) != 500 || Offered(down) != 500 {
		t.Fatalf("up %d down %d", Offered(up), Offered(down))
	}
	if avgGap(up, 0, 10) <= avgGap(up, 489, 10) {
		t.Fatal("increasing rate should pack events toward the end")
	}
	if avgGap(down, 0, 10) >= avgGap(down, 489, 10) {
		t.Fatal("decreasing rate should spread events toward the end")
	}
	spike := scenario.Segment{Start: 0, TimeStamp: 10, Type: "spike", RPSMin: 1000, RPSMax: 3}
	if Offered(spike) != 30 {
		t.Fatalf("spike offered %d", Offered(spike))
	}
}

func TestRoundFractionalOffer(t *testing.T) {
	half := scenario.Segment{Start: 0, TimeStamp: 1, Type: "linear", RPSMin: 0.5, RPSMax: 0.5}
	if Offered(half) != 1 {
		t.Fatalf("0.5 rounded to %d", Offered(half))
	}
	low := scenario.Segment{Start: 0, TimeStamp: 1, Type: "linear", RPSMin: 0.4, RPSMax: 0.4}
	if Offered(low) != 0 {
		t.Fatalf("0.4 rounded to %d", Offered(low))
	}
	zero := scenario.Segment{Start: 0, TimeStamp: 5, Type: "spike", RPSMin: 10, RPSMax: 0}
	if Offered(zero) != 0 {
		t.Fatalf("zero spike %d", Offered(zero))
	}
}

func TestInvertRoundTrip(t *testing.T) {
	segs := []scenario.Segment{
		{Start: 0, TimeStamp: 10, Type: "linear", RPSMin: 0, RPSMax: 100},
		{Start: 0, TimeStamp: 10, Type: "linear", RPSMin: 100, RPSMax: 0},
		{Start: 0, TimeStamp: 10, Type: "linear", RPSMin: 50, RPSMax: 50},
		{Start: 5, TimeStamp: 15, Type: "linear", RPSMin: 1, RPSMax: 9},
		{Start: 0, TimeStamp: 10, Type: "spike", RPSMin: 1000, RPSMax: 3},
	}
	for _, seg := range segs {
		total := offeredExact(seg)
		if total == 0 {
			t.Fatalf("total 0 for %+v", seg)
		}
		for k := 1; k <= 20; k++ {
			target := total * float64(k) / 21
			u := invertCumulative(seg, target)
			got := cumulative(seg, u)
			if math.Abs(got-target) > 1e-6 {
				t.Fatalf("seg %+v target %v got %v (u %v)", seg, target, got, u)
			}
		}
	}
}

func cumulative(seg scenario.Segment, u float64) float64 {
	window := seg.TimeStamp - seg.Start
	if seg.Type == "spike" {
		return seg.RPSMax * u
	}
	return seg.RPSMin*u + (seg.RPSMax-seg.RPSMin)*u*u/(2*window)
}

func avgGap(seg scenario.Segment, from, n int) float64 {
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += EventDue(seg, from+i+1) - EventDue(seg, from+i)
	}
	return sum / float64(n)
}

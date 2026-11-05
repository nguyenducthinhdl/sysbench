package query

import (
	"math"
	"testing"
	"time"

	"syscon-bench/internal/scenario"
)

func TestCheckedInQuerySchedule(t *testing.T) {
	s1, err := scenario.Load("../../log-generator/scenarios/test-scenario-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dues := Schedule(s1)
	if len(dues) != 60 {
		t.Fatalf("scenario 01 ticks %d", len(dues))
	}
	if math.Abs(dues[0]-30) > 1e-9 || math.Abs(dues[59]-3570) > 1e-9 {
		t.Fatalf("ends %v %v", dues[0], dues[59])
	}
	assertInside(t, s1, dues)

	s2, err := scenario.Load("../../log-generator/scenarios/test-scenario-02.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 300, 2600, 2000, 1000}
	sum := 0
	for i, seg := range s2.Segments {
		if Count(seg) != want[i] {
			t.Fatalf("%s count %d", seg.Name, Count(seg))
		}
		sum += Count(seg)
	}
	dues = Schedule(s2)
	if len(dues) != 5901 || sum != 5901 {
		t.Fatalf("scenario 02 ticks %d sum %d", len(dues), sum)
	}
	assertInside(t, s2, dues)
}

func TestQueryCountRounds(t *testing.T) {
	half := scenario.Segment{Start: 0, TimeStamp: 1, QueryRate: 30}
	if Count(half) != 1 || math.Abs(Due(half, 0)-0.5) > 1e-9 {
		t.Fatalf("half count %d due %v", Count(half), Due(half, 0))
	}
	low := scenario.Segment{Start: 0, TimeStamp: 1, QueryRate: 24}
	if Count(low) != 0 {
		t.Fatalf("0.4 rounded to %d", Count(low))
	}
	if Count(scenario.Segment{Start: 0, TimeStamp: 10, QueryRate: 0}) != 0 {
		t.Fatal("zero rate")
	}
	if Schedule(nil) != nil {
		t.Fatal("nil scenario")
	}
	if Due(half, 3) != 0 {
		t.Fatalf("out of range %v", Due(half, 3))
	}
}

func assertInside(t *testing.T, sc *scenario.Scenario, dues []float64) {
	t.Helper()
	prev := -1.0
	si := 0
	for i, due := range dues {
		for si < len(sc.Segments)-1 && due >= sc.Segments[si].TimeStamp {
			si++
		}
		seg := sc.Segments[si]
		if due < seg.Start || due >= seg.TimeStamp || due <= prev {
			t.Fatalf("due %d = %v outside %s [%v, %v)", i, due, seg.Name, seg.Start, seg.TimeStamp)
		}
		if toDur(due) >= toDur(sc.Configs.Duration) {
			t.Fatalf("due %d rounds onto the run end", i)
		}
		prev = due
	}
}

func toDur(s float64) time.Duration {
	return time.Duration(math.Round(s * float64(time.Second)))
}

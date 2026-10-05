package schedule

import (
	"math"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		RPSWindow:   10 * time.Minute,
		ErrorWindow: 15 * time.Minute,
		PanicDelays: []time.Duration{10 * time.Minute, 15 * time.Minute},
	}
}

func TestRouteRPSRamp(t *testing.T) {
	cfg := testConfig()
	start := time.Unix(0, 0).UTC()

	if got := cfg.RouteRPS(start); got != RPSMin {
		t.Fatalf("window start: got %v", got)
	}
	mid := start.Add(5 * time.Minute)
	if math.Abs(cfg.RouteRPS(mid)-110) > 0.001 {
		t.Fatalf("window middle: got %v", cfg.RouteRPS(mid))
	}
	nearEnd := start.Add(cfg.RPSWindow - time.Millisecond)
	got := cfg.RouteRPS(nearEnd)
	if got < 119.9 || got >= RPSMax {
		t.Fatalf("window end: got %v", got)
	}
	if next := cfg.RouteRPS(start.Add(cfg.RPSWindow)); next != RPSMin {
		t.Fatalf("next window: got %v", next)
	}
}

func TestErrorRateRamp(t *testing.T) {
	cfg := testConfig()
	start := time.Unix(0, 0).UTC()

	if got := cfg.ErrorRate(start); got != 0 {
		t.Fatalf("window start: got %v", got)
	}
	mid := start.Add(cfg.ErrorWindow / 2)
	if math.Abs(cfg.ErrorRate(mid)-0.05) > 0.0001 {
		t.Fatalf("window middle: got %v", cfg.ErrorRate(mid))
	}
	nearEnd := start.Add(cfg.ErrorWindow - time.Millisecond)
	got := cfg.ErrorRate(nearEnd)
	if got < 0.099 || got >= ErrorMax {
		t.Fatalf("window end: got %v", got)
	}
	if next := cfg.ErrorRate(start.Add(cfg.ErrorWindow)); next != 0 {
		t.Fatalf("next window: got %v", next)
	}
}

func TestFromEnvOverride(t *testing.T) {
	t.Setenv("RPS_WINDOW", "30s")
	t.Setenv("ERROR_WINDOW", "45s")
	t.Setenv("PANIC_DELAYS", "5s, 10s")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RPSWindow != 30*time.Second || cfg.ErrorWindow != 45*time.Second {
		t.Fatalf("windows: %+v", cfg)
	}
	if len(cfg.PanicDelays) != 2 || cfg.PanicDelays[0] != 5*time.Second || cfg.PanicDelays[1] != 10*time.Second {
		t.Fatalf("delays: %v", cfg.PanicDelays)
	}
}

func TestFromEnvRejectsBadDelay(t *testing.T) {
	t.Setenv("PANIC_DELAYS", "nope")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error")
	}
}

func TestPanicDelayUsesConfiguredSet(t *testing.T) {
	cfg := testConfig()
	seen := map[time.Duration]int{}
	for i := 0; i < 200; i++ {
		d := cfg.PanicDelay()
		seen[d]++
	}
	if seen[10*time.Minute] == 0 || seen[15*time.Minute] == 0 {
		t.Fatalf("delays not both chosen: %v", seen)
	}
}

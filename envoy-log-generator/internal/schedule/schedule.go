// Package schedule is the wall-clock ramp for offered load, backend errors, and panics.
//
// Route RPS moves from 100 to 120 across each RPS window, then starts again at 100.
// Backend error rate moves from 0 to 10% across each error window. Both use the
// wall clock, so a process restart does not rewind the curve.
package schedule

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

const (
	// RPSMin is the offered rate at the start of each route window.
	RPSMin = 100
	// RPSMax is the offered rate at the end of each route window.
	RPSMax = 120
	// ErrorMax is the backend failure probability at the end of each error window.
	ErrorMax = 0.10
)

// Config holds the window lengths. Zero values are not valid; use FromEnv.
type Config struct {
	RPSWindow   time.Duration
	ErrorWindow time.Duration
	PanicDelays []time.Duration
}

// FromEnv reads RPS_WINDOW, ERROR_WINDOW, and PANIC_DELAYS.
// Unset variables use 10m, 15m, and "10m,15m".
func FromEnv() (Config, error) {
	cfg := Config{
		RPSWindow:   10 * time.Minute,
		ErrorWindow: 15 * time.Minute,
		PanicDelays: []time.Duration{10 * time.Minute, 15 * time.Minute},
	}
	var err error
	if v := os.Getenv("RPS_WINDOW"); v != "" {
		cfg.RPSWindow, err = time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RPS_WINDOW: %w", err)
		}
	}
	if v := os.Getenv("ERROR_WINDOW"); v != "" {
		cfg.ErrorWindow, err = time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("ERROR_WINDOW: %w", err)
		}
	}
	if v := os.Getenv("PANIC_DELAYS"); v != "" {
		cfg.PanicDelays, err = parseDelays(v)
		if err != nil {
			return Config{}, fmt.Errorf("PANIC_DELAYS: %w", err)
		}
	}
	if cfg.RPSWindow <= 0 || cfg.ErrorWindow <= 0 {
		return Config{}, fmt.Errorf("windows must be positive")
	}
	return cfg, nil
}

func parseDelays(s string) ([]time.Duration, error) {
	parts := strings.Split(s, ",")
	out := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		d, err := time.ParseDuration(part)
		if err != nil {
			return nil, err
		}
		if d <= 0 {
			return nil, fmt.Errorf("delay %s must be positive", part)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty list")
	}
	return out, nil
}

// RouteRPS is the aggregate rate one route should be offered at now.
func (c Config) RouteRPS(now time.Time) float64 {
	return ramp(now, c.RPSWindow, RPSMin, RPSMax)
}

// ErrorRate is the probability a backend request fails at now.
func (c Config) ErrorRate(now time.Time) float64 {
	return ramp(now, c.ErrorWindow, 0, ErrorMax)
}

// PanicDelay picks one of the configured delays at random.
func (c Config) PanicDelay() time.Duration {
	return c.PanicDelays[rand.Intn(len(c.PanicDelays))]
}

func ramp(now time.Time, window time.Duration, min, max float64) float64 {
	pos := float64(now.UnixNano()%int64(window)) / float64(window)
	return min + (max-min)*pos
}

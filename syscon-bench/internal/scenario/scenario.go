// Package scenario parses and validates a syscon-bench scenario file.
//
// Times are seconds from the start of the run. The last segment time_stamp
// equals duration. rps_min_concurrent and rps_max_concurrent are offered
// events per second for the whole process. concurrent is the worker count.
package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// namePattern is the Prometheus-safe id used for scenario, segment, and
// dashboard names.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// storageNames are the storage ids Phase 1 knows. Adapters for the engines
// arrive with later steps; blackhole is the local HTTP discard target.
var storageNames = map[string]struct{}{
	"blackhole":     {},
	"clickhouse":    {},
	"elasticsearch": {},
	"loki":          {},
	"victorialogs":  {},
}

const (
	minMessageSize = 64
	maxMessageSize = 1048576
)

// Scenario is one validated document.
type Scenario struct {
	Name      string
	Storage   Target
	Dashboard Target
	Configs   Configs
	Segments  []Segment
}

// Target is the single storage or dashboard entry.
type Target struct {
	Name     string
	Endpoint string
	Protocol string
	Auth     Auth
}

// Auth is none or HTTP basic.
type Auth struct {
	Type     string
	Username string
	Password string
}

// Configs is the run-wide size, worker count, and length.
type Configs struct {
	MinSize    int
	MaxSize    int
	Concurrent int
	Duration   float64
}

// Segment is one half-open window [Start, TimeStamp).
// Start is the previous boundary (0 for the first segment). TimeStamp is the
// YAML time_stamp and the window end. Both are seconds from the run start.
type Segment struct {
	Name      string
	Start     float64
	TimeStamp float64
	Type      string
	RPSMin    float64
	RPSMax    float64
	ErrorRate float64
	QueryRate float64
}

type rawFile struct {
	Name      string       `yaml:"name"`
	Storage   []rawTarget  `yaml:"storage"`
	Dashboard []rawTarget  `yaml:"dashboard"`
	Configs   rawConfigs   `yaml:"configs"`
	Scenario  []rawSegment `yaml:"scenario"`
}

type rawTarget struct {
	Name     string  `yaml:"name"`
	Endpoint string  `yaml:"endpoint"`
	Protocol string  `yaml:"protocol"`
	Auth     rawAuth `yaml:"auth"`
}

type rawAuth struct {
	Type     string `yaml:"type"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type rawConfigs struct {
	MinSize    float64 `yaml:"min_size"`
	MaxSize    float64 `yaml:"max_size"`
	Concurrent float64 `yaml:"concurrent"`
	Duration   float64 `yaml:"duration"`
}

type rawSegment struct {
	Name      string  `yaml:"name"`
	TimeStamp float64 `yaml:"time_stamp"`
	Type      string  `yaml:"type"`
	RPSMin    float64 `yaml:"rps_min_concurrent"`
	RPSMax    float64 `yaml:"rps_max_concurrent"`
	ErrorRate float64 `yaml:"error_rate"`
	QueryRate float64 `yaml:"query_rate"`
}

// Load reads and validates a scenario file.
func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	sc, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}

// Parse validates a scenario document.
func Parse(b []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var raw rawFile
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("scenario: empty file")
		}
		return nil, fmt.Errorf("scenario: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("scenario: multiple documents")
		}
		return nil, fmt.Errorf("scenario: %w", err)
	}
	return raw.convert()
}

func (r rawFile) convert() (*Scenario, error) {
	if err := checkName("name", r.Name); err != nil {
		return nil, err
	}
	storage, err := oneTarget("storage", r.Storage, true)
	if err != nil {
		return nil, err
	}
	dashboard, err := oneTarget("dashboard", r.Dashboard, false)
	if err != nil {
		return nil, err
	}
	cfg, err := r.Configs.convert()
	if err != nil {
		return nil, err
	}
	segments, err := convertSegments(r.Scenario, cfg.Duration)
	if err != nil {
		return nil, err
	}
	return &Scenario{
		Name:      r.Name,
		Storage:   storage,
		Dashboard: dashboard,
		Configs:   cfg,
		Segments:  segments,
	}, nil
}

// ScaleDuration stretches or compresses the clock to seconds.
// Ingest and query rates stay as written. The last boundary equals the new duration.
func (s *Scenario) ScaleDuration(seconds float64) error {
	if s == nil {
		return errors.New("scenario: nil scenario")
	}
	if err := finite("duration", seconds); err != nil {
		return err
	}
	if seconds <= 0 {
		return errors.New("scenario: duration must be > 0")
	}
	if len(s.Segments) == 0 || s.Configs.Duration <= 0 {
		return errors.New("scenario: duration must be > 0")
	}
	factor := seconds / s.Configs.Duration
	for i := range s.Segments {
		s.Segments[i].Start *= factor
		s.Segments[i].TimeStamp *= factor
	}
	s.Segments[len(s.Segments)-1].TimeStamp = seconds
	s.Configs.Duration = seconds
	return nil
}

// OverrideStorage replaces the storage name and endpoint.
// Protocol is set from the endpoint scheme. Auth is left as parsed.
// name and endpoint must both be non-empty; the caller enforces the pair.
func (s *Scenario) OverrideStorage(name, endpoint string) error {
	if s == nil {
		return errors.New("storage: nil scenario")
	}
	if _, ok := storageNames[name]; !ok {
		return fmt.Errorf("storage: name %q must be blackhole, clickhouse, elasticsearch, loki, or victorialogs", name)
	}
	scheme, err := httpScheme("storage", endpoint)
	if err != nil {
		return err
	}
	s.Storage.Name = name
	s.Storage.Endpoint = endpoint
	s.Storage.Protocol = scheme
	return nil
}

func oneTarget(field string, items []rawTarget, storage bool) (Target, error) {
	if len(items) != 1 {
		return Target{}, fmt.Errorf("scenario: %s must contain exactly one entry", field)
	}
	raw := items[0]
	if storage {
		if _, ok := storageNames[raw.Name]; !ok {
			return Target{}, fmt.Errorf("scenario: %s name %q must be blackhole, clickhouse, elasticsearch, loki, or victorialogs", field, raw.Name)
		}
	} else if err := checkName(field+" name", raw.Name); err != nil {
		return Target{}, err
	}
	scheme, err := httpScheme(field, raw.Endpoint)
	if err != nil {
		return Target{}, err
	}
	if raw.Protocol != scheme {
		return Target{}, fmt.Errorf("scenario: %s protocol %q must match endpoint scheme %q", field, raw.Protocol, scheme)
	}
	auth, err := convertAuth(field, raw.Auth)
	if err != nil {
		return Target{}, err
	}
	return Target{
		Name:     raw.Name,
		Endpoint: raw.Endpoint,
		Protocol: scheme,
		Auth:     auth,
	}, nil
}

func httpScheme(field, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("scenario: %s endpoint must be an absolute http or https URL", field)
	}
	return u.Scheme, nil
}

func convertAuth(field string, raw rawAuth) (Auth, error) {
	switch raw.Type {
	case "none":
		if raw.Username != "" || raw.Password != "" {
			return Auth{}, fmt.Errorf("scenario: %s auth type none must omit username and password", field)
		}
		return Auth{Type: "none"}, nil
	case "basic":
		if raw.Username == "" || raw.Password == "" {
			return Auth{}, fmt.Errorf("scenario: %s auth type basic requires username and password", field)
		}
		return Auth{Type: "basic", Username: raw.Username, Password: raw.Password}, nil
	default:
		return Auth{}, fmt.Errorf("scenario: %s auth type must be none or basic", field)
	}
}

func (c rawConfigs) convert() (Configs, error) {
	minSize, err := whole("min_size", c.MinSize, minMessageSize, maxMessageSize)
	if err != nil {
		return Configs{}, err
	}
	maxSize, err := whole("max_size", c.MaxSize, minMessageSize, maxMessageSize)
	if err != nil {
		return Configs{}, err
	}
	if maxSize < minSize {
		return Configs{}, fmt.Errorf("scenario: max_size must be >= min_size (%d)", minSize)
	}
	workers, err := whole("concurrent", c.Concurrent, 1, math.MaxInt)
	if err != nil {
		return Configs{}, err
	}
	if err := finite("duration", c.Duration); err != nil {
		return Configs{}, err
	}
	if c.Duration <= 0 {
		return Configs{}, errors.New("scenario: duration must be > 0")
	}
	return Configs{
		MinSize:    minSize,
		MaxSize:    maxSize,
		Concurrent: workers,
		Duration:   c.Duration,
	}, nil
}

func convertSegments(raw []rawSegment, duration float64) ([]Segment, error) {
	if len(raw) == 0 {
		return nil, errors.New("scenario: at least one segment is required")
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]Segment, 0, len(raw))
	prev := 0.0
	for _, seg := range raw {
		if err := checkName("segment name", seg.Name); err != nil {
			return nil, err
		}
		if _, ok := seen[seg.Name]; ok {
			return nil, fmt.Errorf("scenario: duplicate segment name %q", seg.Name)
		}
		seen[seg.Name] = struct{}{}
		if err := finite("time_stamp", seg.TimeStamp); err != nil {
			return nil, fmt.Errorf("scenario: segment %q: %w", seg.Name, err)
		}
		if seg.TimeStamp <= prev {
			return nil, fmt.Errorf("scenario: segment %q time_stamp must be greater than %g", seg.Name, prev)
		}
		if seg.Type != "linear" && seg.Type != "spike" {
			return nil, fmt.Errorf("scenario: segment %q type must be linear or spike", seg.Name)
		}
		if err := nonNegative("rps_min_concurrent", seg.RPSMin); err != nil {
			return nil, fmt.Errorf("segment %q: %w", seg.Name, err)
		}
		if err := nonNegative("rps_max_concurrent", seg.RPSMax); err != nil {
			return nil, fmt.Errorf("segment %q: %w", seg.Name, err)
		}
		if err := unitInterval(seg.ErrorRate); err != nil {
			return nil, fmt.Errorf("segment %q: %w", seg.Name, err)
		}
		if err := nonNegative("query_rate", seg.QueryRate); err != nil {
			return nil, fmt.Errorf("segment %q: %w", seg.Name, err)
		}
		out = append(out, Segment{
			Name:      seg.Name,
			Start:     prev,
			TimeStamp: seg.TimeStamp,
			Type:      seg.Type,
			RPSMin:    seg.RPSMin,
			RPSMax:    seg.RPSMax,
			ErrorRate: seg.ErrorRate,
			QueryRate: seg.QueryRate,
		})
		prev = seg.TimeStamp
	}
	if prev != duration {
		return nil, fmt.Errorf("scenario: last time_stamp (%g) must equal duration (%g)", prev, duration)
	}
	return out, nil
}

func checkName(field, name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("scenario: %s %q must match %s", field, name, namePattern.String())
	}
	return nil
}

func whole(field string, v float64, min, max int) (int, error) {
	if err := finite(field, v); err != nil {
		return 0, err
	}
	if v != math.Trunc(v) {
		return 0, fmt.Errorf("scenario: %s must be an integer", field)
	}
	if v < float64(min) || v > float64(max) {
		return 0, fmt.Errorf("scenario: %s must be in [%d, %d]", field, min, max)
	}
	return int(v), nil
}

func finite(field string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("scenario: %s must be a finite number", field)
	}
	return nil
}

func nonNegative(field string, v float64) error {
	if err := finite(field, v); err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("scenario: %s must be >= 0", field)
	}
	return nil
}

func unitInterval(v float64) error {
	if err := finite("error_rate", v); err != nil {
		return err
	}
	if v < 0 || v > 1 {
		return errors.New("scenario: error_rate must be in [0, 1]")
	}
	return nil
}

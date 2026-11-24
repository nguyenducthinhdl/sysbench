// Package metrics registers the bench series and renders them as Prometheus text.
package metrics

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// ContentType is the Prometheus text exposition media type, version 0.0.4.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// PushInterval is how often the process posts the series, in scenario time.
const PushInterval = 15 * time.Second

// Query duration buckets, in seconds.
var queryBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// QueryClasses are the histogram and error-counter label values.
var QueryClasses = []string{"fulltext", "exact", "filter", "aggregation"}

// Metrics are the five client series for one storage and scenario.
type Metrics struct {
	registerer prometheus.Registerer
	gatherer   prometheus.Gatherer
	collectors []prometheus.Collector

	events    prometheus.Counter
	bytes     prometheus.Counter
	errors    prometheus.Counter
	queryDur  *prometheus.HistogramVec
	queryErrs *prometheus.CounterVec
}

// New registers the series. storage and scenario are the Prometheus labels.
// reg and gather are usually prometheus.DefaultRegisterer and DefaultGatherer.
func New(reg prometheus.Registerer, gather prometheus.Gatherer, storageName, scenarioName string) (*Metrics, error) {
	if reg == nil || gather == nil {
		return nil, errors.New("metrics: registerer and gatherer are required")
	}
	if storageName == "" || scenarioName == "" {
		return nil, errors.New("metrics: storage and scenario are required")
	}
	labels := prometheus.Labels{
		"storage":  storageName,
		"scenario": scenarioName,
	}
	m := &Metrics{
		registerer: reg,
		gatherer:   gather,
		events: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "bench_ingest_events_total",
			Help:        "Events the sink accepted.",
			ConstLabels: labels,
		}),
		bytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "bench_ingest_bytes_total",
			Help:        "UTF-8 message bytes of events the sink accepted.",
			ConstLabels: labels,
		}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "bench_ingest_errors_total",
			Help:        "Events the sink did not accept.",
			ConstLabels: labels,
		}),
		queryDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "bench_query_duration_seconds",
			Help:        "Seconds from query start until the body is fully read.",
			ConstLabels: labels,
			Buckets:     queryBuckets,
		}, []string{"query_class"}),
		queryErrs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "bench_query_errors_total",
			Help:        "Queries that timed out or returned a non-2xx status.",
			ConstLabels: labels,
		}, []string{"query_class"}),
	}
	for _, class := range QueryClasses {
		m.queryDur.WithLabelValues(class)
		m.queryErrs.WithLabelValues(class)
	}
	m.collectors = []prometheus.Collector{m.events, m.bytes, m.errors, m.queryDur, m.queryErrs}
	registered := make([]prometheus.Collector, 0, len(m.collectors))
	for _, c := range m.collectors {
		if err := reg.Register(c); err != nil {
			for _, done := range registered {
				reg.Unregister(done)
			}
			return nil, err
		}
		registered = append(registered, c)
	}
	return m, nil
}

// Unregister removes the series. Safe to call more than once.
func (m *Metrics) Unregister() {
	if m == nil {
		return
	}
	for _, c := range m.collectors {
		m.registerer.Unregister(c)
	}
}

// IngestAccepted counts events the sink accepted and their message bytes.
func (m *Metrics) IngestAccepted(events, messageBytes int) {
	if m == nil || events <= 0 {
		return
	}
	m.events.Add(float64(events))
	if messageBytes > 0 {
		m.bytes.Add(float64(messageBytes))
	}
}

// IngestFailed counts events in a batch the sink did not accept.
func (m *Metrics) IngestFailed(events int) {
	if m == nil || events <= 0 {
		return
	}
	m.errors.Add(float64(events))
}

// ObserveQuery records one search.
// bodyRead adds the duration. failed increments the error counter.
// A timeout is failed without bodyRead. A fully read non-2xx body is both.
func (m *Metrics) ObserveQuery(class string, seconds float64, bodyRead, failed bool) {
	if m == nil {
		return
	}
	if bodyRead {
		m.queryDur.WithLabelValues(class).Observe(seconds)
	}
	if failed {
		m.queryErrs.WithLabelValues(class).Inc()
	}
}

// Text renders the bench_* series as Prometheus text exposition 0.0.4.
func (m *Metrics) Text() ([]byte, error) {
	families, err := m.gatherer.Gather()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, family := range families {
		if family == nil || !strings.HasPrefix(family.GetName(), "bench_") {
			continue
		}
		if err := enc.Encode(family); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

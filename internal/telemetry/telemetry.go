package telemetry

import (
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	PlannerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "travelmcp_planner_duration_seconds",
		Help:    "Planner duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"engine"})

	ProviderHealth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "travelmcp_provider_health",
		Help: "Provider up 1/0",
	}, []string{"provider"})

	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "travelmcp_http_requests_total",
		Help: "HTTP requests",
	}, []string{"code", "handler"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "travelmcp_http_request_duration_seconds",
		Help:    "HTTP request latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"code", "handler"})

	ExternalRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "travelmcp_external_requests_total",
		Help: "External requests",
	}, []string{"host", "status"})
)

type Metrics struct {
	mu       sync.Mutex
	counters map[string]int64
}

func ObservePlanner(engine string, d time.Duration) {
	PlannerDuration.WithLabelValues(engine).Observe(d.Seconds())
}

func SetProviderHealth(provider string, up bool) {
	v := 0.0
	if up {
		v = 1
	}
	ProviderHealth.WithLabelValues(provider).Set(v)
}

func New() *Metrics {
	return &Metrics{counters: map[string]int64{}}
}

func (m *Metrics) Inc(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name]++
}

func (m *Metrics) IncN(name string, n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += n
}

func (m *Metrics) Snapshot() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.counters))
	for k, v := range m.counters {
		out[k] = v
	}
	return out
}

func (m *Metrics) Named() []Named {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Named, 0, len(m.counters))
	for k, v := range m.counters {
		out = append(out, Named{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type Named struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

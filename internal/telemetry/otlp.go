package telemetry

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type OTLPMetricsHandler struct {
	registry *prometheus.Registry
}

func NewOTLPMetricsHandler(r *prometheus.Registry) *OTLPMetricsHandler {
	if r == nil {
		r = prometheus.DefaultRegisterer.(*prometheus.Registry)
	}
	return &OTLPMetricsHandler{registry: r}
}

type otlpMetric struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Value  float64 `json:"value,omitempty"`
	Count  int     `json:"count,omitempty"`
	Sum    float64 `json:"sum,omitempty"`
	Labels []label `json:"labels,omitempty"`
}

type label struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type otlpResponseBody struct {
	ResourceMetrics []resourceMetrics `json:"resourceMetrics"`
}

type resourceMetrics struct {
	Resource struct {
		Attributes []attribute `json:"attributes,omitempty"`
	} `json:"resource"`
	ScopeMetrics []scopeMetrics `json:"scopeMetrics"`
}

type scopeMetrics struct {
	Metrics []otlpMetric `json:"metrics"`
}

type attribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (h *OTLPMetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/metrics/otlp" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "POST, GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mfs, err := h.registry.Gather()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	resp := otlpResponseBody{}
	rm := resourceMetrics{}
	rm.Resource.Attributes = []attribute{{Key: "service.name", Value: "travelmcp"}}
	for _, mf := range mfs {
		mfname, mfhelp := mf.GetName(), mf.GetHelp()
		for _, m := range mf.Metric {
			ot := toOTLPMetric(mfname, mfhelp, m)
			rm.ScopeMetrics = append(rm.ScopeMetrics, scopeMetrics{Metrics: []otlpMetric{ot}})
		}
	}
	resp.ResourceMetrics = append(resp.ResourceMetrics, rm)
	writeJSON(w, http.StatusOK, resp)
}

func toOTLPMetric(name, help string, m *dto.Metric) otlpMetric {
	var ot otlpMetric
	ot.Name = name
	ot.Labels = make([]label, 0, len(m.Label))
	for _, l := range m.Label {
		ot.Labels = append(ot.Labels, label{Key: l.GetName(), Value: l.GetValue()})
	}
	switch {
	case m.Histogram != nil:
		ot.Type = "histogram"
		ot.Count = int(m.Histogram.GetSampleCount())
		ot.Sum = m.Histogram.GetSampleSum()
	case m.Counter != nil:
		ot.Type = "counter"
		ot.Value = m.GetCounter().GetValue()
		ot.Count = 1
	case m.Gauge != nil:
		ot.Type = "gauge"
		ot.Value = m.GetGauge().GetValue()
		ot.Count = 1
	default:
		ot.Type = "gauge"
	}
	return ot
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data, err := json.Marshal(v); err == nil {
		_, _ = w.Write(data)
	}
}

func init() {
	_ = strconv.Itoa
	_ = dto.Metric{}
}

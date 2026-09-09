package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestOTLPMetricsHandler(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_counter",
		Help: "test",
	})
	reg.MustRegister(c)
	c.Add(42)

	h := NewOTLPMetricsHandler(reg)
	req := httptest.NewRequest("GET", "/api/v1/metrics/otlp", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	rms, ok := resp["resourceMetrics"].([]any)
	if !ok || len(rms) == 0 {
		t.Fatal("expected resourceMetrics")
	}
}

func TestOTLPMetricsHandlerWrongPath(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewOTLPMetricsHandler(reg)
	req := httptest.NewRequest("GET", "/wrong", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

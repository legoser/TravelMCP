package telemetry

import (
	"sort"
	"sync"
)

type Metrics struct {
	mu       sync.Mutex
	counters map[string]int64
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

package providers

import (
	"log/slog"
	"sync"
	"time"

	"travelmcp/internal/model"
)

type HealthStatus struct {
	Up             bool      `json:"up"`
	LastImportTime time.Time `json:"last_import_time,omitempty"`
	Records        int       `json:"records"`
	LastError      string    `json:"last_error,omitempty"`
	Issues         int       `json:"issues"`
	ExcludedStops  int       `json:"excluded_stops"`
}

type Capabilities struct {
	SupportsFares    bool
	SupportsRealtime bool
	Modes            []model.Mode
}

type Provider interface {
	ID() string
	Health() HealthStatus
	Network() (*model.Network, error)
	Capabilities() Capabilities
}

type Registry struct {
	mu         sync.RWMutex
	byID       map[string]Provider
	order      []string
	snapshot   map[string]HealthStatus
	snapshotAt time.Time
}

func NewRegistry(enabled []string) *Registry {
	return NewRegistryWithLogger(enabled, nil)
}

// NewRegistryWithLogger собирает реестр из включённых провайдеров.
// Legacy JSON-провайдер intercity вырезан (Фаза 6): канон строится
// конвейером skeleton-sync + trips-sync в Store, планировщик читает
// Store, fallback-сеть собирается из активных провайдеров реестра.
func NewRegistryWithLogger(enabled []string, logger *slog.Logger) *Registry {
	r := &Registry{byID: map[string]Provider{}}
	if len(enabled) == 0 {
		return r
	}
	for _, id := range enabled {
		r.enable(id)
	}
	return r
}

var factories = map[string]func() Provider{
	SynthID: func() Provider { return NewSynth(time.Now()) },
	GTFSID:  func() Provider { return NewGTFS("") },
}

func RegisterFactory(id string, fn func() Provider) {
	factories[id] = fn
}

func (r *Registry) enable(id string) {
	if fn, ok := factories[id]; ok {
		r.Register(fn())
	}
}

func (r *Registry) Register(p Provider) {
	if _, ok := r.byID[p.ID()]; ok {
		return
	}
	r.byID[p.ID()] = p
	r.order = append(r.order, p.ID())
}

func (r *Registry) Get(id string) (Provider, bool) {
	p, ok := r.byID[id]
	return p, ok
}

func (r *Registry) List() []Provider {
	out := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

func (r *Registry) HealthStatuses() map[string]HealthStatus {
	r.mu.RLock()
	if time.Since(r.snapshotAt) < 10*time.Second && r.snapshot != nil {
		cp := copyStatuses(r.snapshot)
		r.mu.RUnlock()
		return cp
	}
	r.mu.RUnlock()
	out := map[string]HealthStatus{}
	for _, id := range r.order {
		out[id] = r.byID[id].Health()
	}
	r.mu.Lock()
	r.snapshot = copyStatuses(out)
	r.snapshotAt = time.Now()
	r.mu.Unlock()
	return copyStatuses(out)
}

func (r *Registry) HealthStatusesCached() map[string]HealthStatus {
	r.mu.RLock()
	if r.snapshot != nil && time.Since(r.snapshotAt) < 10*time.Second {
		cp := copyStatuses(r.snapshot)
		r.mu.RUnlock()
		return cp
	}
	r.mu.RUnlock()
	return r.HealthStatuses()
}

func copyStatuses(m map[string]HealthStatus) map[string]HealthStatus {
	cp := make(map[string]HealthStatus, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

package providers

import (
	"time"

	"travelmcp/internal/model"
)

type HealthStatus struct {
	Up             bool      `json:"up"`
	LastImportTime time.Time `json:"last_import_time,omitempty"`
	Records        int       `json:"records"`
	LastError      string    `json:"last_error,omitempty"`
}

type Provider interface {
	ID() string
	Health() HealthStatus
	Network() (*model.Network, error)
}

type Registry struct {
	byID          map[string]Provider
	order         []string
	intercityPath string
}

func NewRegistry(enabled []string) *Registry {
	return NewRegistryWith(enabled, "")
}

func NewRegistryWith(enabled []string, intercityPath string) *Registry {
	r := &Registry{byID: map[string]Provider{}, intercityPath: intercityPath}
	if len(enabled) == 0 {
		return r
	}
	for _, id := range enabled {
		r.enable(id)
	}
	return r
}

func (r *Registry) enable(id string) {
	switch id {
	case SynthID:
		r.Register(NewSynth(time.Now()))
	case IntercityID:
		r.Register(NewIntercity(r.intercityPath, time.Now()))
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
	out := map[string]HealthStatus{}
	for _, id := range r.order {
		out[id] = r.byID[id].Health()
	}
	return out
}

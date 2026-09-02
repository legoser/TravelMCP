package ports

import (
	"context"
	"time"

	"travelmcp/internal/model"
)

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

type HealthStatus struct {
	Up             bool
	LastImportTime time.Time
	Records        int
	LastError      string
	Issues         int
	ExcludedStops  int
}

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type Queue interface {
	Enqueue(ctx context.Context, topic string, payload []byte) error
}

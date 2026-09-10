package geocoder

import (
	"fmt"
	"sync"

	"travelmcp/internal/config"
	"travelmcp/internal/support/httpx"
)

var (
	mu       sync.RWMutex
	registry = map[string]func(config.Config, *httpx.Client) Geocoder{}
)

func Register(kind string, fn func(config.Config, *httpx.Client) Geocoder) {
	mu.Lock()
	defer mu.Unlock()
	registry[kind] = fn
}

func New(cfg config.Config, client *httpx.Client) (Geocoder, error) {
	mu.RLock()
	defer mu.RUnlock()
	if len(registry) == 0 {
		return nil, fmt.Errorf("геокодер: нет зарегистрированных провайдеров")
	}
	providers := make(map[string]Geocoder, len(registry))
	for kind, fn := range registry {
		providers[kind] = fn(cfg, client)
	}
	attempts := cfg.Geocoder.Attempts
	if attempts <= 0 {
		attempts = defaultAttempts
	}
	if cfg.Geocoder.Kind != "" {
		if _, ok := registry[cfg.Geocoder.Kind]; !ok {
			return nil, fmt.Errorf("geocoder kind %q not registered", cfg.Geocoder.Kind)
		}
	}
	return NewFallback(providers, attempts, cfg.Geocoder.Kind), nil
}

func NewSingle(cfg config.Config, client *httpx.Client, kind string) (Geocoder, error) {
	mu.RLock()
	defer mu.RUnlock()
	fn, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("geocoder kind %q not registered (available: %v)", kind, registeredKindsLocked())
	}
	return fn(cfg, client), nil
}

func registeredKindsLocked() []string {
	kinds := make([]string, 0, len(registry))
	for k := range registry {
		kinds = append(kinds, k)
	}
	return kinds
}
func RegisteredKinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	return registeredKindsLocked()
}

package geocoder

import (
	"fmt"
	"sync"

	"travelmcp/internal/config"
	"travelmcp/internal/httpx"
)

var (
	mu       sync.RWMutex
	registry = map[string]func(config.Geocoder, *httpx.Client) Geocoder{}
)

func Register(kind string, fn func(config.Geocoder, *httpx.Client) Geocoder) {
	mu.Lock()
	defer mu.Unlock()
	registry[kind] = fn
}

func New(cfg config.Geocoder, client *httpx.Client) (Geocoder, error) {
	kind := cfg.Kind
	if kind == "" {
		kind = "yandex"
	}
	mu.RLock()
	fn, ok := registry[kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("geocoder kind %q not registered", kind)
	}
	return fn(cfg, client), nil
}

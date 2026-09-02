package nominatim

import (
	"context"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/httpx"
)

type Geocoder struct {
	cfg    config.Yandex
	client *httpx.Client
}

func New(cfg config.Yandex, client *httpx.Client) *Geocoder {
	return &Geocoder{cfg: cfg, client: client}
}

var _ geocoder.Geocoder = (*Geocoder)(nil)

func init() {
	geocoder.Register("nominatim", func(cfg config.Yandex, client *httpx.Client) geocoder.Geocoder {
		return New(cfg, client)
	})
}

func (g *Geocoder) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	// TODO: implement Nominatim https://nominatim.openstreetmap.org/search?q=...&format=json
	return &geocoder.Result{Name: query}, nil
}

func (g *Geocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	return "", nil
}

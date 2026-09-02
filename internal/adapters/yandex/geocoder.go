package yandex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/httpx"
)

func init() {
	geocoder.Register("yandex", func(cfg config.Config, client *httpx.Client) geocoder.Geocoder {
		return New(cfg, client)
	})
}

type Geocoder struct {
	cfg    config.Config
	client *httpx.Client
	url    string
	key    string
}

func New(cfg config.Config, client *httpx.Client) *Geocoder {
	url := cfg.Yandex.GeocodeURL
	if url == "" {
		url = cfg.Geocoder.URL
	}
	if url == "" {
		url = "https://geocode-maps.yandex.ru/1.x"
	}
	key := cfg.Yandex.GeocodeKey
	if key == "" {
		key = cfg.Geocoder.ApiKey
	}
	if key == "" {
		key = cfg.Geocoder.Key
	}
	return &Geocoder{cfg: cfg, client: client, url: url, key: key}
}

var _ geocoder.Geocoder = (*Geocoder)(nil)

type yandexResponse struct {
	Response struct {
		GeoObjectCollection struct {
			FeatureMember []struct {
				GeoObject struct {
					Point struct {
						Pos string `json:"pos"`
					} `json:"Point"`
				} `json:"GeoObject"`
			} `json:"featureMember"`
		} `json:"GeoObjectCollection"`
	} `json:"response"`
}

func (y *Geocoder) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	if y.key == "" {
		return nil, fmt.Errorf("geocode key empty")
	}
	base, err := url.Parse(y.url)
	if err != nil {
		return nil, fmt.Errorf("geocode url: %w", err)
	}
	q := base.Query()
	q.Set("format", "json")
	q.Set("apikey", y.key)
	q.Set("geocode", query)
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	resp, err := y.client.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocode %d", resp.StatusCode)
	}
	var data yandexResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Response.GeoObjectCollection.FeatureMember) == 0 {
		return nil, fmt.Errorf("not found")
	}
	var lon, lat float64
	fmt.Sscan(data.Response.GeoObjectCollection.FeatureMember[0].GeoObject.Point.Pos, &lon, &lat)
	return &geocoder.Result{Lat: lat, Lon: lon, Name: query}, nil
}

func (y *Geocoder) Reverse(_ context.Context, lat, lon float64) (string, error) {
	return fmt.Sprintf("%.5f,%.5f", lat, lon), nil
}

package yandex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/support/httpx"
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
var _ geocoder.MultiGeocoder = (*Geocoder)(nil)

type yandexResponse struct {
	Response struct {
		GeoObjectCollection struct {
			FeatureMember []struct {
				GeoObject struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Point       struct {
						Pos string `json:"pos"`
					} `json:"Point"`
				} `json:"GeoObject"`
			} `json:"featureMember"`
		} `json:"GeoObjectCollection"`
	} `json:"response"`
}

func (y *Geocoder) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	cands, err := y.GeocodeCandidates(ctx, query, 1)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return &geocoder.Result{Lat: cands[0].Lat, Lon: cands[0].Lon, Name: cands[0].Name}, nil
}

func (y *Geocoder) GeocodeCandidates(ctx context.Context, query string, limit int) ([]geocoder.Candidate, error) {
	if y.key == "" {
		return nil, fmt.Errorf("geocode key empty")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	base, err := url.Parse(y.url)
	if err != nil {
		return nil, fmt.Errorf("geocode url: %w", err)
	}
	q := base.Query()
	q.Set("format", "json")
	q.Set("apikey", y.key)
	q.Set("geocode", query)
	q.Set("results", fmt.Sprintf("%d", limit))
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
	var out []geocoder.Candidate
	for _, fm := range data.Response.GeoObjectCollection.FeatureMember {
		var lon, lat float64
		fmt.Sscan(fm.GeoObject.Point.Pos, &lon, &lat)
		name := fm.GeoObject.Name
		if name == "" {
			name = query
		}
		out = append(out, geocoder.Candidate{Lat: lat, Lon: lon, Name: name, Provider: "yandex"})
		if len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return out, nil
}

func (y *Geocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	name, desc, err := y.reverseParts(ctx, lat, lon)
	if err != nil {
		return "", err
	}
	if desc == "" {
		return name, nil
	}
	return name + ", " + desc, nil
}

func (y *Geocoder) ReverseSettlement(ctx context.Context, lat, lon float64) (string, error) {
	_, desc, err := y.reverseParts(ctx, lat, lon)
	if err != nil {
		return "", err
	}
	if st := strings.SplitN(desc, ",", 2)[0]; strings.TrimSpace(st) != "" {
		return strings.TrimSpace(st), nil
	}
	return "", fmt.Errorf("not found")
}

func (y *Geocoder) reverseParts(ctx context.Context, lat, lon float64) (string, string, error) {
	if y.key == "" {
		return "", "", fmt.Errorf("geocode key empty")
	}
	base, err := url.Parse(y.url)
	if err != nil {
		return "", "", fmt.Errorf("geocode url: %w", err)
	}
	q := base.Query()
	q.Set("format", "json")
	q.Set("apikey", y.key)
	q.Set("geocode", fmt.Sprintf("%v,%v", lon, lat))
	q.Set("results", "1")
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	resp, err := y.client.Do(ctx, req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("geocode %d", resp.StatusCode)
	}
	var data yandexResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", "", err
	}
	fm := data.Response.GeoObjectCollection.FeatureMember
	if len(fm) == 0 {
		return "", "", fmt.Errorf("not found")
	}
	return fm[0].GeoObject.Name, fm[0].GeoObject.Description, nil
}

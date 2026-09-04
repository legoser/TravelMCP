package nominatim

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/support/httpx"
)

type Geocoder struct {
	cfg    config.Config
	client *httpx.Client
	url    string
}

func New(cfg config.Config, client *httpx.Client) *Geocoder {
	u := cfg.Nominatim.URL
	if u == "" {
		u = "https://nominatim.openstreetmap.org"
	}
	return &Geocoder{cfg: cfg, client: client, url: u}
}

var _ geocoder.Geocoder = (*Geocoder)(nil)

func init() {
	geocoder.Register("nominatim", func(cfg config.Config, client *httpx.Client) geocoder.Geocoder {
		return New(cfg, client)
	})
}

func (g *Geocoder) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	base, err := url.Parse(g.url + "/search")
	if err != nil {
		return nil, fmt.Errorf("nominatim url: %w", err)
	}
	q := base.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("limit", "1")
	q.Set("accept-language", "ru")
	q.Set("addressdetails", "0")
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	req.Header.Set("User-Agent", "travelmcp/1.0 (https://github.com/anomalyco/travelmcp)")
	resp, err := g.client.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim %d", resp.StatusCode)
	}
	var data []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("not found")
	}
	lat, _ := strconv.ParseFloat(data[0].Lat, 64)
	lon, _ := strconv.ParseFloat(data[0].Lon, 64)
	return &geocoder.Result{Lat: lat, Lon: lon, Name: data[0].DisplayName}, nil
}

func (g *Geocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	base, err := url.Parse(g.url + "/reverse")
	if err != nil {
		return "", fmt.Errorf("nominatim url: %w", err)
	}
	q := base.Query()
	q.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(lon, 'f', 6, 64))
	q.Set("format", "json")
	q.Set("accept-language", "ru")
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	req.Header.Set("User-Agent", "travelmcp/1.0 (https://github.com/anomalyco/travelmcp)")
	resp, err := g.client.Do(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nominatim %d", resp.StatusCode)
	}
	var data struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.DisplayName == "" {
		return "", fmt.Errorf("not found")
	}
	return data.DisplayName, nil
}

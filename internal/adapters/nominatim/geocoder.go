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

// Локальные значения (не конфиг): протокол Nominatim search API.
// defaultResultsLimit — кандидатов при limit<=0, если geocoder.limit не задан;
// maxResultsLimit — потолок (больше API всё равно режет).
const (
	defaultNominatimURL = "https://nominatim.openstreetmap.org"
	defaultResultsLimit = 5
	maxResultsLimit     = 10
	acceptLanguage      = "ru"
)

func New(cfg config.Config, client *httpx.Client) *Geocoder {
	u := cfg.Nominatim.URL
	if u == "" {
		u = defaultNominatimURL
	}
	return &Geocoder{cfg: cfg, client: client, url: u}
}

var _ geocoder.Geocoder = (*Geocoder)(nil)
var _ geocoder.MultiGeocoder = (*Geocoder)(nil)

func init() {
	geocoder.Register("nominatim", func(cfg config.Config, client *httpx.Client) geocoder.Geocoder {
		return New(cfg, client)
	})
}

func (g *Geocoder) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	cands, err := g.GeocodeCandidates(ctx, query, 1)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return &geocoder.Result{Lat: cands[0].Lat, Lon: cands[0].Lon, Name: cands[0].Name}, nil
}

func (g *Geocoder) GeocodeCandidates(ctx context.Context, query string, limit int) ([]geocoder.Candidate, error) {
	if limit <= 0 {
		limit = g.cfg.Geocoder.Limit
	}
	if limit <= 0 {
		limit = defaultResultsLimit
	}
	if limit > maxResultsLimit {
		limit = maxResultsLimit
	}
	base, err := url.Parse(g.url + "/search")
	if err != nil {
		return nil, fmt.Errorf("nominatim url: %w", err)
	}
	q := base.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("accept-language", acceptLanguage)
	q.Set("addressdetails", "0")
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	req.Header.Set("User-Agent", geocoder.DefaultUserAgent)
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
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("not found")
	}
	var out []geocoder.Candidate
	for _, d := range data {
		lat, _ := strconv.ParseFloat(d.Lat, 64)
		lon, _ := strconv.ParseFloat(d.Lon, 64)
		name := d.Name
		if name == "" {
			name = d.DisplayName
		}
		out = append(out, geocoder.Candidate{Lat: lat, Lon: lon, Name: name, Provider: "nominatim"})
		if len(out) >= limit {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return out, nil
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
	q.Set("accept-language", acceptLanguage)
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	req.Header.Set("User-Agent", geocoder.DefaultUserAgent)
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

func (g *Geocoder) ReverseSettlement(ctx context.Context, lat, lon float64) (string, error) {
	base, err := url.Parse(g.url + "/reverse")
	if err != nil {
		return "", fmt.Errorf("nominatim url: %w", err)
	}
	q := base.Query()
	q.Set("lat", strconv.FormatFloat(lat, 'f', 6, 64))
	q.Set("lon", strconv.FormatFloat(lon, 'f', 6, 64))
	q.Set("format", "json")
	q.Set("accept-language", acceptLanguage)
	q.Set("addressdetails", "1")
	base.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	req.Header.Set("User-Agent", geocoder.DefaultUserAgent)
	resp, err := g.client.Do(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nominatim %d", resp.StatusCode)
	}
	var data struct {
		Address map[string]string `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	for _, k := range []string{"city", "town", "village", "municipality", "hamlet", "county"} {
		if v := data.Address[k]; v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("not found")
}

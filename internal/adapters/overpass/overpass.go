package overpass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
	"travelmcp/internal/support/httpx"
)

const (
	DefaultURL       = "https://overpass-api.de/api/interpreter"
	DefaultMirrorURL = "https://overpass.openstreetmap.fr/api/interpreter"
)

func init() {
	geocoder.Register("overpass", func(cfg config.Config, client *httpx.Client) geocoder.Geocoder {
		return New(cfg, client)
	})
}

type Adapter struct {
	cfg      config.Config
	client   *httpx.Client
	baseURLs []string
}

func New(cfg config.Config, client *httpx.Client) *Adapter {
	u := cfg.Overpass.URL
	if u == "" {
		u = DefaultURL
	}
	m := cfg.Overpass.MirrorURL
	if m == "" {
		m = DefaultMirrorURL
	}
	return &Adapter{cfg: cfg, client: client, baseURLs: []string{u, m}}
}

var _ geocoder.Geocoder = (*Adapter)(nil)
var _ geocoder.MultiGeocoder = (*Adapter)(nil)

func (a *Adapter) BaseURLs() []string {
	out := make([]string, len(a.baseURLs))
	copy(out, a.baseURLs)
	return out
}

type BBox struct {
	MinLat float64
	MinLon float64
	MaxLat float64
	MaxLon float64
}

func (b BBox) String() string {
	return fmt.Sprintf("%.6f,%.6f,%.6f,%.6f", b.MinLat, b.MinLon, b.MaxLat, b.MaxLon)
}

func BuildStopsQuery(b BBox, timeoutS int) string {
	if timeoutS <= 0 {
		timeoutS = 60
	}
	return fmt.Sprintf(`[out:json][timeout:%d];
(
  node["public_transport"~"platform|station|stop_position"](%s);
  node["highway"="bus_stop"](%s);
  node["railway"~"station|halt"](%s);
);
out tags;`,
		timeoutS, b.String(), b.String(), b.String())
}

// EscapeQLString screens special characters in literal string values within Overpass QL quotes.
func EscapeQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// BuildSearchByNameQuery constructs an Overpass QL query looking for public transport
// nodes/ways/relations by name in the given bbox or globally if bbox is empty.
func BuildSearchByNameQuery(name string, b *BBox, timeoutS int) string {
	if timeoutS <= 0 {
		timeoutS = 30
	}
	escaped := EscapeQLString(name)
	bboxFilter := ""
	if b != nil {
		bboxFilter = fmt.Sprintf("(%s)", b.String())
	}
	return fmt.Sprintf(`[out:json][timeout:%d];
(
  node["name"="%s"]["highway"~"bus_stop|bus_station"]%s;
  node["name"="%s"]["public_transport"~"platform|station|stop_position"]%s;
  node["name"="%s"]["railway"~"station|halt"]%s;
);
out center 10;`,
		timeoutS,
		escaped, bboxFilter,
		escaped, bboxFilter,
		escaped, bboxFilter,
	)
}

type overpassElement struct {
	Type   string   `json:"type"`
	ID     int64    `json:"id"`
	Lat    *float64 `json:"lat,omitempty"`
	Lon    *float64 `json:"lon,omitempty"`
	Center *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"center,omitempty"`
	Tags map[string]string `json:"tags,omitempty"`
}

type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
}

func (a *Adapter) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	cands, err := a.GeocodeCandidates(ctx, query, 1)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return &geocoder.Result{
		Lat:  cands[0].Lat,
		Lon:  cands[0].Lon,
		Name: cands[0].Name,
	}, nil
}

func (a *Adapter) GeocodeCandidates(ctx context.Context, query string, limit int) ([]geocoder.Candidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty query")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	ql := BuildSearchByNameQuery(query, nil, 30)
	respBody, err := a.execQL(ctx, ql)
	if err != nil {
		return nil, err
	}

	var data overpassResponse
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("overpass json parse: %w", err)
	}

	var out []geocoder.Candidate
	for _, el := range data.Elements {
		var lat, lon float64
		if el.Lat != nil && el.Lon != nil {
			lat = *el.Lat
			lon = *el.Lon
		} else if el.Center != nil {
			lat = el.Center.Lat
			lon = el.Center.Lon
		} else {
			continue
		}

		name := el.Tags["name"]
		if name == "" {
			name = query
		}
		out = append(out, geocoder.Candidate{
			Lat:      lat,
			Lon:      lon,
			Name:     name,
			Provider: "overpass",
		})
		if len(out) >= limit {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("not found")
	}
	return out, nil
}

func (a *Adapter) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	return "", errors.New("reverse geocoding not supported by overpass adapter")
}

// BuildAroundQuery constructs an Overpass QL query for public-transport nodes within
// radiusMeters of the given point. Default radius is 500m when radiusMeters <= 0.
func BuildAroundQuery(lat, lon float64, radiusMeters int) string {
	if radiusMeters <= 0 {
		radiusMeters = 500
	}
	return fmt.Sprintf(`[out:json][timeout:60];
node(around:%d,%.6f,%.6f)
  ["public_transport"~"platform|station|stop_position"];
node(around:%d,%.6f,%.6f)
  ["highway"="bus_stop"];
node(around:%d,%.6f,%.6f)
  ["railway"~"station|halt"];
out center;`,
		radiusMeters, lat, lon,
		radiusMeters, lat, lon,
		radiusMeters, lat, lon)
}

// StationsAround queries Overpass for public-transport nodes within radiusMeters of
// the given point and returns the parsed AdaptedRecords. It performs a single
// network request with automatic fallback to the mirror endpoint.
func (a *Adapter) StationsAround(ctx context.Context, lat, lon float64, radiusMeters int) ([]model.AdaptedRecord, error) {
	if radiusMeters <= 0 {
		radiusMeters = 500
	}
	ql := BuildAroundQuery(lat, lon, radiusMeters)
	body, err := a.execQL(ctx, ql)
	if err != nil {
		return nil, err
	}
	return ParseResponse(body)
}

func (a *Adapter) execQL(ctx context.Context, ql string) ([]byte, error) {
	urls := a.BaseURLs()
	if len(urls) == 0 {
		return nil, errors.New("no overpass endpoints configured")
	}

	var lastErr error
	for _, rawURL := range urls {
		body, err := a.doPost(ctx, rawURL, ql)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("overpass all endpoints failed: %w", lastErr)
}

func (a *Adapter) doPost(ctx context.Context, rawURL, ql string) ([]byte, error) {
	reqData := url.Values{}
	reqData.Set("data", ql)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(reqData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "travelmcp/1.0 (https://github.com/anomalyco/travelmcp)")

	var resp *http.Response
	if a.client != nil {
		resp, err = a.client.Do(ctx, req)
	} else {
		resp, err = http.DefaultClient.Do(req)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("overpass status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	return io.ReadAll(resp.Body)
}

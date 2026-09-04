package motis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(baseURL string, httpClient *http.Client) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

func NewWithBase(baseURL string) *Client { return New(baseURL, nil) }

func (c *Client) url(path string) string { return c.baseURL + path }

func (c *Client) doGet(ctx context.Context, path string, query url.Values, out any) error {
	u := c.url(path)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var er ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		if er.Error != "" {
			return fmt.Errorf("motis %s %d: %s", path, resp.StatusCode, er.Error)
		}
		return fmt.Errorf("motis %s %d", path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	if err := c.doGet(ctx, "/api/v1/health", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Initial(ctx context.Context) (*InitialResponse, error) {
	var out InitialResponse
	if err := c.doGet(ctx, "/api/v1/map/initial", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type GeocodeParams struct {
	Text       string
	Language   []string
	Type       []LocationType
	NumResults *int
	Place      *string
	PlaceBias  *float64
	Min        *string
	Max        *string
}

func (c *Client) Geocode(ctx context.Context, p GeocodeParams) ([]Match, error) {
	if strings.TrimSpace(p.Text) == "" {
		return nil, fmt.Errorf("geocode: text required")
	}
	q := url.Values{}
	q.Set("text", p.Text)
	for _, l := range p.Language {
		q.Add("language", l)
	}
	for _, t := range p.Type {
		q.Add("type", string(t))
	}
	if p.NumResults != nil {
		q.Set("numResults", strconv.Itoa(*p.NumResults))
	}
	if p.Place != nil {
		q.Set("place", *p.Place)
	}
	if p.PlaceBias != nil {
		q.Set("placeBias", strconv.FormatFloat(*p.PlaceBias, 'f', -1, 64))
	}
	if p.Min != nil {
		q.Set("min", *p.Min)
	}
	if p.Max != nil {
		q.Set("max", *p.Max)
	}
	var out []Match
	if err := c.doGet(ctx, "/api/v1/geocode", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type ReverseParams struct {
	Place      string
	Type       []LocationType
	NumResults *int
}

func (c *Client) ReverseGeocode(ctx context.Context, p ReverseParams) ([]Match, error) {
	if strings.TrimSpace(p.Place) == "" {
		return nil, fmt.Errorf("reverse-geocode: place required")
	}
	q := url.Values{}
	q.Set("place", p.Place)
	for _, t := range p.Type {
		q.Add("type", string(t))
	}
	if p.NumResults != nil {
		q.Set("numResults", strconv.Itoa(*p.NumResults))
	}
	var out []Match
	if err := c.doGet(ctx, "/api/v1/reverse-geocode", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) MapStops(ctx context.Context, min, max string) ([]Place, error) {
	if min == "" || max == "" {
		return nil, fmt.Errorf("map/stops: min and max required")
	}
	q := url.Values{}
	q.Set("min", min)
	q.Set("max", max)
	var out []Place
	if err := c.doGet(ctx, "/api/v6/map/stops", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) MapLevels(ctx context.Context, min, max string) ([]float64, error) {
	q := url.Values{}
	q.Set("min", min)
	q.Set("max", max)
	var out []float64
	if err := c.doGet(ctx, "/api/v1/map/levels", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type PlanParams struct {
	FromPlace    string
	ToPlace      string
	Time         *time.Time
	ArriveBy     *bool
	MaxTransfers *int
	TransitModes []Mode
	Language     []string
	Radius       *float64
}

func (c *Client) Plan(ctx context.Context, p PlanParams) (*PlanResponse, error) {
	if strings.TrimSpace(p.FromPlace) == "" || strings.TrimSpace(p.ToPlace) == "" {
		return nil, fmt.Errorf("plan: fromPlace and toPlace required")
	}
	q := url.Values{}
	q.Set("fromPlace", p.FromPlace)
	q.Set("toPlace", p.ToPlace)
	if p.Time != nil {
		q.Set("time", p.Time.Format(time.RFC3339))
	}
	if p.ArriveBy != nil {
		q.Set("arriveBy", strconv.FormatBool(*p.ArriveBy))
	}
	if p.MaxTransfers != nil {
		q.Set("maxTransfers", strconv.Itoa(*p.MaxTransfers))
	}
	if len(p.TransitModes) > 0 {
		vals := make([]string, len(p.TransitModes))
		for i, m := range p.TransitModes {
			vals[i] = string(m)
		}
		q.Set("transitModes", strings.Join(vals, ","))
	}
	for _, l := range p.Language {
		q.Add("language", l)
	}
	if p.Radius != nil {
		q.Set("radius", strconv.FormatFloat(*p.Radius, 'f', -1, 64))
	}
	var out PlanResponse
	if err := c.doGet(ctx, "/api/v6/plan", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func ValidateMatch(m Match) error {
	if m.Name == "" {
		return fmt.Errorf("match name empty")
	}
	if m.ID == "" {
		return fmt.Errorf("match id empty")
	}
	if m.Lat < -90 || m.Lat > 90 || m.Lon < -180 || m.Lon > 180 {
		return fmt.Errorf("match coords out of range %f,%f", m.Lat, m.Lon)
	}
	if m.Tz != nil && *m.Tz == "" {
		return fmt.Errorf("match tz empty")
	}
	if m.Importance != nil && (*m.Importance < 0 || *m.Importance > 1) {
		return fmt.Errorf("importance out of [0,1] %f", *m.Importance)
	}
	for _, a := range m.Areas {
		if a.Name == "" {
			return fmt.Errorf("area name empty")
		}
		if a.AdminLevel < 1 || a.AdminLevel > 12 {
			return fmt.Errorf("adminLevel %v out of 1..12", a.AdminLevel)
		}
	}
	return nil
}

func ValidatePlace(p Place) error {
	if p.Name == "" {
		return fmt.Errorf("place name empty")
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return fmt.Errorf("place coords out of range")
	}
	if p.Importance != nil && (*p.Importance < 0 || *p.Importance > 1) {
		return fmt.Errorf("importance out of range")
	}
	return nil
}

package geocoder

import "context"

type Result struct {
	Lat  float64
	Lon  float64
	Name string
}

type Candidate struct {
	Lat      float64
	Lon      float64
	Name     string
	Provider string
}

type Geocoder interface {
	Geocode(ctx context.Context, query string) (*Result, error)
	Reverse(ctx context.Context, lat, lon float64) (string, error)
}

type MultiGeocoder interface {
	Geocoder
	GeocodeCandidates(ctx context.Context, query string, limit int) ([]Candidate, error)
}

type SettlementReverser interface {
	ReverseSettlement(ctx context.Context, lat, lon float64) (string, error)
}

package geocoder

import "context"

type Result struct {
	Lat  float64
	Lon  float64
	Name string
}

type Geocoder interface {
	Geocode(ctx context.Context, query string) (*Result, error)
	Reverse(ctx context.Context, lat, lon float64) (string, error)
}

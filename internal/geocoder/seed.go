package geocoder

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type SeedEntry struct {
	QueryNorm string
	Lat       float64
	Lon       float64
}

func ParseSeedFile(path string) ([]SeedEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string][2]float64
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("seed %s: %w", path, err)
	}
	out := make([]SeedEntry, 0, len(raw))
	for q, ll := range raw {
		out = append(out, SeedEntry{QueryNorm: NormalizeQuery(q), Lat: ll[0], Lon: ll[1]})
	}
	return out, nil
}

func SeedInto(store CacheStore, provider, path string, at time.Time) (int, error) {
	entries, err := ParseSeedFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, e := range entries {
		store.Set(e.QueryNorm, provider, CacheEntry{Lat: e.Lat, Lon: e.Lon, ObservedAt: at, Origin: "seed"})
	}
	return len(entries), nil
}

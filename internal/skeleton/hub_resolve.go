package skeleton

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"travelmcp/internal/adapters/overpass"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
)

var ErrHubNotResolved = errors.New("hub not resolved")

type HubResolver struct {
	osmAdapter *overpass.Adapter
	yanSource  *YandexDumpSource
	nominatim  geocoder.Geocoder
	overpass   *overpass.Adapter
	cache      *geocoder.MapGeoCacheStore
}

type HubResolverOption func(*HubResolver)

func WithNominatim(g geocoder.Geocoder) HubResolverOption {
	return func(r *HubResolver) { r.nominatim = g }
}

// hubCacheTTL — TTL in-memory кэша хаб-координат (сутки: хабы стабильны,
// свежие правки подтянутся следующим прогоном).
const hubCacheTTL = 24 * time.Hour

func NewHubResolver(osmAdapter *overpass.Adapter, yanSource *YandexDumpSource, overpassAdapter *overpass.Adapter) *HubResolver {
	return &HubResolver{
		osmAdapter: osmAdapter,
		yanSource:  yanSource,
		overpass:   overpassAdapter,
		cache:      geocoder.NewMapGeoCacheStore(hubCacheTTL),
	}
}

type HubCoords struct {
	Name   string
	Lat    float64
	Lon    float64
	Source string
}

func (r *HubResolver) Resolve(ctx context.Context, name string, aliases ...string) (*HubCoords, error) {
	key := fmt.Sprintf("hub:%s", name)
	if e, ok := r.cache.Get(key); ok && len(e.Records) > 0 {
		rec := e.Records[0]
		if rec.Lat != nil && rec.Lon != nil {
			return &HubCoords{Name: name, Lat: *rec.Lat, Lon: *rec.Lon, Source: e.Origin}, nil
		}
	}

	candidates := r.resolveChain(ctx, name, aliases)
	for _, c := range candidates {
		if c == nil {
			continue
		}
		lat, lon := c.Lat, c.Lon
		r.cache.Set(key, geocoder.GeoCacheEntry{
			Records: []model.AdaptedRecord{{NameRu: c.Name, Lat: &lat, Lon: &lon}},
			Origin:  c.Source,
		})
		return c, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrHubNotResolved, name)
}

func (r *HubResolver) resolveChain(ctx context.Context, name string, aliases []string) []*HubCoords {
	sources := []struct {
		name string
		fn   func(context.Context, string, []string) *HubCoords
	}{
		{"osm_local", r.resolveOSM},
		{"yandex_dump", r.resolveYandex},
		{"nominatim", r.resolveNominatim},
		{"overpass", r.resolveOverpass},
	}

	for _, s := range sources {
		if s.fn == nil {
			continue
		}
		c := s.fn(ctx, name, aliases)
		if c != nil {
			slog.Debug("hub resolved", "name", name, "source", s.name, "lat", c.Lat, "lon", c.Lon)
			return []*HubCoords{c}
		}
	}
	return nil
}

func (r *HubResolver) resolveOSM(ctx context.Context, name string, aliases []string) *HubCoords {
	if r.osmAdapter == nil {
		return nil
	}
	cands, err := r.osmAdapter.GeocodeCandidates(ctx, name, 3)
	if err != nil || len(cands) == 0 {
		return nil
	}
	for _, c := range cands {
		if c.Lat != 0 && c.Lon != 0 {
			return &HubCoords{Name: c.Name, Lat: c.Lat, Lon: c.Lon, Source: "osm"}
		}
	}
	return nil
}

func (r *HubResolver) resolveYandex(ctx context.Context, name string, aliases []string) *HubCoords {
	if r.yanSource == nil {
		return nil
	}
	records, err := r.yanSource.Load()
	if err != nil {
		return nil
	}
	norm := normalize(name)
	for _, rec := range records {
		if normalize(rec.NameRu) == norm {
			if rec.Lat != nil && rec.Lon != nil {
				return &HubCoords{Name: rec.NameRu, Lat: *rec.Lat, Lon: *rec.Lon, Source: "yandex"}
			}
		}
	}
	for _, alias := range aliases {
		aNorm := normalize(alias)
		for _, rec := range records {
			if normalize(rec.NameRu) == aNorm {
				if rec.Lat != nil && rec.Lon != nil {
					return &HubCoords{Name: rec.NameRu, Lat: *rec.Lat, Lon: *rec.Lon, Source: "yandex"}
				}
			}
		}
	}
	return nil
}

func (r *HubResolver) resolveNominatim(ctx context.Context, name string, aliases []string) *HubCoords {
	if r.nominatim == nil {
		return nil
	}
	queries := append([]string{name}, aliases...)
	for _, query := range queries {
		var cands []geocoder.Candidate
		if mc, ok := r.nominatim.(geocoder.MultiGeocoder); ok {
			var err error
			cands, err = mc.GeocodeCandidates(ctx, query, 3)
			if err != nil || len(cands) == 0 {
				continue
			}
		} else {
			res, err := r.nominatim.Geocode(ctx, query)
			if err != nil {
				continue
			}
			cands = []geocoder.Candidate{{Name: res.Name, Lat: res.Lat, Lon: res.Lon}}
		}
		for _, c := range cands {
			if c.Lat != 0 && c.Lon != 0 {
				return &HubCoords{Name: c.Name, Lat: c.Lat, Lon: c.Lon, Source: "nominatim"}
			}
		}
	}
	return nil
}

func (r *HubResolver) resolveOverpass(ctx context.Context, name string, aliases []string) *HubCoords {
	if r.overpass == nil {
		return nil
	}
	for _, query := range append([]string{name}, aliases...) {
		cands, err := r.overpass.GeocodeCandidates(ctx, query, 5)
		if err != nil || len(cands) == 0 {
			continue
		}
		for _, c := range cands {
			if c.Lat != 0 && c.Lon != 0 {
				return &HubCoords{Name: c.Name, Lat: c.Lat, Lon: c.Lon, Source: "overpass"}
			}
		}
	}
	return nil
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(
		" автовокзал", "", " автостанция", "",
		" аэропорт", "", " вокзал", "",
		" станция", "",
	).Replace(s)
	return s
}

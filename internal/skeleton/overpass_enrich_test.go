package skeleton

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
)

type fakeStationsProviderForEnrich struct {
	mu     sync.Mutex
	calls  int
	coords []struct{ lat, lon float64 }
	resp   []model.AdaptedRecord
	err    error
}

func (f *fakeStationsProviderForEnrich) StationsAround(ctx context.Context, lat, lon float64, radiusM int) ([]model.AdaptedRecord, error) {
	f.mu.Lock()
	f.calls++
	f.coords = append(f.coords, struct{ lat, lon float64 }{lat, lon})
	f.mu.Unlock()
	return f.resp, f.err
}

func (f *fakeStationsProviderForEnrich) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestOverpassEnrichNoProvider(t *testing.T) {
	outcome := &JoinOutcome{
		Canon:      []JoinedRecord{{Record: model.AdaptedRecord{NameRu: "A"}}},
		Unverified: []model.AdaptedRecord{{NameRu: "B"}},
	}
	n := OverpassEnrich(context.Background(), outcome, nil, 200, slog.Default())
	if n != 0 {
		t.Errorf("want 0 with nil provider, got %d", n)
	}
	if len(outcome.Canon) != 1 {
		t.Errorf("Canon should be unchanged, got %d", len(outcome.Canon))
	}
}

func TestOverpassEnrichRecovers(t *testing.T) {
	prov := &fakeStationsProviderForEnrich{
		resp: []model.AdaptedRecord{
			{
				NameRu: "Найденная остановка",
				Lat:    float64Ptr(55.5),
				Lon:    float64Ptr(86.1),
				Identifiers: []model.AdaptedIdentifier{
					{System: "osm", CodeType: "osm_id", Code: "999"},
				},
				Extra: map[string]string{
					"transport_type": "bus",
					"settlement":     "Кемерово",
				},
			},
		},
	}
	cached := geocoder.NewCachedStationsProvider(prov, nil, nil, 0)

	lat := 55.5
	outcome := &JoinOutcome{
		Canon: []JoinedRecord{{Record: model.AdaptedRecord{NameRu: "Уже канон"}}},
		Unverified: []model.AdaptedRecord{
			{NameRu: "Первая", Lat: &lat, Lon: &lat},
			{NameRu: "Вторая", Lat: &lat, Lon: &lat},
		},
	}

	n := OverpassEnrich(context.Background(), outcome, cached, 200, slog.Default())
	if n != 2 {
		t.Errorf("want 2 recovered, got %d", n)
	}
	if len(outcome.Canon) != 3 {
		t.Errorf("want 3 Canon (1 before + 2 recovered), got %d", len(outcome.Canon))
	}
	if len(outcome.Unverified) != 0 {
		t.Errorf("want 0 Unverified (both recovered), got %d: %v", len(outcome.Unverified), outcome.Unverified)
	}
}

func TestOverpassEnrichMaxPoints(t *testing.T) {
	prov := &fakeStationsProviderForEnrich{
		resp: []model.AdaptedRecord{{NameRu: "О"}},
	}
	cached := geocoder.NewCachedStationsProvider(prov, nil, nil, 0)

	lat := 55.0
	outcome := &JoinOutcome{
		Unverified: []model.AdaptedRecord{
			{NameRu: "A", Lat: &lat, Lon: &lat},
			{NameRu: "B", Lat: &lat, Lon: &lat},
			{NameRu: "C", Lat: &lat, Lon: &lat},
		},
	}

	OverpassEnrich(context.Background(), outcome, cached, 2, slog.Default())
	if prov.Calls() != 2 {
		t.Errorf("want 2 calls (max 2), got %d", prov.Calls())
	}
}

func TestOverpassEnrichSkipsNoCoords(t *testing.T) {
	prov := &fakeStationsProviderForEnrich{resp: []model.AdaptedRecord{{NameRu: "O"}}}
	cached := geocoder.NewCachedStationsProvider(prov, nil, nil, 0)

	outcome := &JoinOutcome{
		Unverified: []model.AdaptedRecord{
			{NameRu: "Без координат"},
		},
	}

	n := OverpassEnrich(context.Background(), outcome, cached, 200, slog.Default())
	if n != 0 {
		t.Errorf("want 0 recovered (no coords), got %d", n)
	}
	if prov.Calls() != 0 {
		t.Errorf("want 0 provider calls for no-coords record, got %d", prov.Calls())
	}
}

func float64Ptr(v float64) *float64 { return &v }

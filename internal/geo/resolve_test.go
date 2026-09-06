package geo

import (
	"context"
	"strings"
	"testing"

	"travelmcp/internal/geocoder"
)

type stubGeocoder struct {
	candidates []geocoder.Candidate
	queries    []string
	calls      int
	err        error
}

func (s *stubGeocoder) Geocode(ctx context.Context, query string) (*geocoder.Result, error) {
	cands, err := s.GeocodeCandidates(ctx, query, 1)
	if err != nil {
		return nil, err
	}
	return &geocoder.Result{Lat: cands[0].Lat, Lon: cands[0].Lon, Name: cands[0].Name}, nil
}

func (s *stubGeocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	return "", nil
}

func (s *stubGeocoder) GeocodeCandidates(ctx context.Context, query string, limit int) ([]geocoder.Candidate, error) {
	s.calls++
	s.queries = append(s.queries, query)
	if s.err != nil {
		return nil, s.err
	}
	return s.candidates, nil
}

func TestResolverQueriesRegionAndSettlement(t *testing.T) {
	stub := &stubGeocoder{candidates: []geocoder.Candidate{{Lat: 56.5, Lon: 84.9, Name: "Стрежевой"}}}
	r := NewGeoResolver(stub, 5, 0.8, 10)
	lat, lon, src, _, ok := r.Resolve(context.Background(), "Автостанция г. Стрежевой", "70")
	if !ok || src != "geocode" || lat == 0 || lon == 0 {
		t.Fatalf("want geocode coords, got ok=%v src=%q lat=%v", ok, src, lat)
	}
	if len(stub.queries) != 1 {
		t.Fatalf("want 1 api call, got %d", len(stub.queries))
	}
	q := strings.ToLower(stub.queries[0])
	if !strings.Contains(q, "томская область") || !strings.Contains(q, "стрежевой") {
		t.Fatalf("query must contain region+settlement, got %q", stub.queries[0])
	}
}

func TestResolverNameGateRejects(t *testing.T) {
	stub := &stubGeocoder{candidates: []geocoder.Candidate{{Lat: 55.7, Lon: 37.6, Name: "Москва"}}}
	r := NewGeoResolver(stub, 5, 0.8, 10)
	_, _, _, _, ok := r.Resolve(context.Background(), "Автостанция г. Стрежевой", "70")
	if ok {
		t.Fatalf("irrelevant candidate must be rejected by name-gate")
	}
}

func TestResolverNoSettlementNoAPI(t *testing.T) {
	stub := &stubGeocoder{candidates: []geocoder.Candidate{{Lat: 1, Lon: 2, Name: "ЛПК"}}}
	r := NewGeoResolver(stub, 5, 0.8, 10)
	for _, name := range []string{"ОП «ЛПК»", "АВ «Центральный»", "ОП п. Аэропорт", "АС «Автостанция»"} {
		if _, _, _, _, ok := r.Resolve(context.Background(), name, "25"); ok {
			t.Fatalf("Resolve(%q) should not match", name)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("facility names must not hit API, got %d calls", stub.calls)
	}
}

func TestResolverCacheAndCap(t *testing.T) {
	stub := &stubGeocoder{candidates: []geocoder.Candidate{{Lat: 56.5, Lon: 84.9, Name: "Стрежевой"}}}
	r := NewGeoResolver(stub, 5, 0.8, 10)
	r.Resolve(context.Background(), "Автостанция г. Стрежевой", "70")
	r.Resolve(context.Background(), "ДКП г. Стрежевой", "70")
	if stub.calls != 1 {
		t.Fatalf("same settlement must be cached, got %d calls", stub.calls)
	}

	stub2 := &stubGeocoder{candidates: []geocoder.Candidate{{Lat: 1, Lon: 2, Name: "Яя"}}}
	r2 := NewGeoResolver(stub2, 5, 0.8, 1)
	r2.Resolve(context.Background(), "ОП «Кассовый пункт пгт Яя»", "42")
	r2.Resolve(context.Background(), "Остановочный пункт п. Новый Быт", "66")
	if stub2.calls != 1 {
		t.Fatalf("maxCalls=1 must cap API calls, got %d", stub2.calls)
	}
}

func TestRegionName(t *testing.T) {
	if got := RegionName("54"); got != "Новосибирская область" {
		t.Fatalf("RegionName(54) = %q", got)
	}
	if got := RegionName("25"); got != "Приморский край" {
		t.Fatalf("RegionName(25) = %q", got)
	}
	if got := RegionName("99"); got != "" {
		t.Fatalf("unknown code must be empty, got %q", got)
	}
}

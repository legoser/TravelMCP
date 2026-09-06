package memory

import (
	"context"
	"testing"

	store "travelmcp/internal/store"
)

func TestUpsertRouteIdempotentByNK(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	id1, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "42", LongName: "A"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "42", LongName: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("ресинк обязан быть upsert по (source, external_route_code), а не дублем: %d != %d", id1, id2)
	}
	if len(m.routes) != 1 {
		t.Fatalf("повторный прогон не должен накапливать дубли routes: %d", len(m.routes))
	}
}

func TestUpsertTripIdempotentByNK(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	id1, err := m.UpsertTrip(ctx, store.TripRow{RouteID: 7, ProviderID: "mintrans", ExternalTripCode: "synthetic:a:1:0"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := m.UpsertTrip(ctx, store.TripRow{RouteID: 7, ProviderID: "mintrans", ExternalTripCode: "synthetic:a:1:0"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("ресинк обязан быть upsert по (route_id, external_trip_code), а не дублем: %d != %d", id1, id2)
	}
	if len(m.trips) != 1 {
		t.Fatalf("повторный прогон не должен накапливать дубли trips: %d", len(m.trips))
	}
}

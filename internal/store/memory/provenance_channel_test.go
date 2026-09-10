package memory

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

func TestSaveProvenanceRequiresChannel(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	if err := m.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: 1, Source: "osm", Confidence: 0.5, ObservedAt: time.Now()}); err == nil {
		t.Fatal("запись provenance без channel обязана падать (gate полноты §3.3 молча позеленеет)")
	}
	if err := m.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: 1, Source: "osm", Confidence: 0.5, ObservedAt: time.Now(), Channel: "bogus"}); err == nil {
		t.Fatal("неканонический channel обязан падать")
	}
	if err := m.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: 1, Source: "osm", Confidence: 0.5, ObservedAt: time.Now(), Channel: model.ChannelLocalFile}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckProvenanceCompleteness(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	routeID, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "42"})
	if err != nil {
		t.Fatal(err)
	}
	tripA, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "a"})
	if err != nil {
		t.Fatal(err)
	}
	tripB, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "b"})
	if err != nil {
		t.Fatal(err)
	}

	// Ни одной записи: gate ловит всё
	missing, err := m.CheckProvenanceCompleteness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 3 {
		t.Fatalf("без provenance gate обязан найти route+2 трипа, получил %v", missing)
	}

	// Полная пара на все сущности: gate pass
	for _, p := range []model.Provenance{
		{EntityType: "route", EntityID: routeID, Source: "mintrans", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile},
		{EntityType: "trip", EntityID: tripA, Source: "mintrans", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile},
		{EntityType: "trip", EntityID: tripB, Source: "mintrans", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile},
	} {
		if err := m.SaveProvenance(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	missing, err = m.CheckProvenanceCompleteness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("gate обязан пропустить полный канон, получил %v", missing)
	}

	// Tombstoned-трип не проверяется (планировщику не виден)
	if err := m.TombstoneTrip(ctx, tripB); err != nil {
		t.Fatal(err)
	}
	missing, err = m.CheckProvenanceCompleteness(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("tombstoned трип вне gate, получил %v", missing)
	}
}

func TestListProvenanceChannels(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	if err := m.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: 5, Source: "osm", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile}); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: 6, Source: "yandex", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalMotis}); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListProvenanceChannels(ctx, "terminal", []int64{5, 6, 7})
	if err != nil {
		t.Fatal(err)
	}
	if got[5] != model.ChannelLocalFile || got[6] != model.ChannelLocalMotis {
		t.Fatalf("каналы по ID: %v", got)
	}
	if _, ok := got[7]; ok {
		t.Fatal("сущность без provenance не должна попадать в карту (signal gate)")
	}
}

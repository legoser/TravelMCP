package gtfs

import (
	"context"
	"strings"
	"testing"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

// TestCompilerProvenanceGateBlocks — hard-gate §3.3/§6: zip не собирается,
// пока живые routes/trips канона без пары source/channel в provenance.
// Пустой канон (0 живых сущностей) проходит gate — проверять нечего.
func TestCompilerProvenanceGateBlocks(t *testing.T) {
	m := memory.NewMemoryStore()
	ctx := context.Background()

	if _, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "gov-registry", ExternalRouteCode: "42"}); err != nil {
		t.Fatal(err)
	}
	comp := NewCompiler("f-ru")
	if _, err := comp.BuildPerRegionFromStore(ctx, m, "test", time.Now()); err == nil {
		t.Fatal("gate обязан блокировать сборку zip без provenance-записей")
	} else if !strings.Contains(err.Error(), "provenance gate") {
		t.Fatalf("ожидалась ошибка gate, получил: %v", err)
	}
}

func TestCompilerProvenanceGatePass(t *testing.T) {
	m := memory.NewMemoryStore()
	ctx := context.Background()

	routeID, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "gov-registry", ExternalRouteCode: "42"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "gov-registry", ExternalTripCode: "a"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []model.Provenance{
		{EntityType: "route", EntityID: routeID, Source: "gov-registry", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile},
	} {
		if err := m.SaveProvenance(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// trip всё ещё без provenance: gate блокирует
	comp := NewCompiler("f-ru")
	if _, err := comp.BuildPerRegionFromStore(ctx, m, "test", time.Now()); err == nil {
		t.Fatal("gate обязан ловить trip без provenance")
	}
}

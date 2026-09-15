package mcp

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/store/memory"
)

func TestRecordDemandAsync(t *testing.T) {
	ctx := context.Background()
	ms := memory.NewMemoryStore()
	routeID, err := ms.UpsertRoute(ctx, memory.RouteRow{ProviderID: "dm", ExternalRouteCode: "r1", Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ms.UpsertTrip(ctx, memory.TripRow{RouteID: routeID, ProviderID: "dm", ExternalTripCode: "t1"}); err != nil {
		t.Fatal(err)
	}
	a := &App{store: ms}
	j := &model.Journey{Legs: []model.Leg{
		{TripID: "r1|t1", ProviderID: "dm"},
		{TripID: "r1|t1", ProviderID: "dm"},
		{Mode: model.ModeWalk},
		{TripID: "no-pipe-format", ProviderID: "dm"},
		{TripID: "rx|tx", ProviderID: "dm"},
	}}
	a.recordDemand(ctx, j)
	deadline := time.Now().Add(3 * time.Second)
	for {
		stale, err := ms.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(stale) == 1 && stale[0].RequestCount == 1 {
			return
		}
		if len(stale) > 1 {
			t.Fatalf("dedup failed, want 1 demanded trip: %+v", stale)
		}
		if time.Now().After(deadline) {
			t.Fatalf("demand tick never landed: %+v", stale)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRecordDemandNilSafe(t *testing.T) {
	a := &App{}
	a.recordDemand(context.Background(), nil)
	a.recordDemand(context.Background(), &model.Journey{})
	ms := memory.NewMemoryStore()
	(&App{store: ms}).recordDemand(context.Background(), &model.Journey{
		Legs: []model.Leg{{TripID: "synth-trip-without-pipe"}}},
	)
}

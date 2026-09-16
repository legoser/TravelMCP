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
		{TripID: "r1|t1", ProviderID: "dm", RouteID: "r1"},
		{TripID: "r1|t1", ProviderID: "dm", RouteID: "r1"},
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

func TestSplitDemandTripIDPipeRoute(t *testing.T) {
	l := model.Leg{TripID: "a|b|t1", RouteID: "a|b", ProviderID: "dm"}
	route, ext := splitDemandTripID(l)
	if route != "a|b" || ext != "t1" {
		t.Fatalf("prefix split: route=%q ext=%q, want a|b/t1", route, ext)
	}
	l2 := model.Leg{TripID: "r1|t1", ProviderID: "dm"}
	if route, ext := splitDemandTripID(l2); route != "r1" || ext != "t1" {
		t.Fatalf("fallback split: route=%q ext=%q", route, ext)
	}
}

func TestRecordDemandBatchPipeRoute(t *testing.T) {
	ctx := context.Background()
	ms := memory.NewMemoryStore()
	routeID, err := ms.UpsertRoute(ctx, memory.RouteRow{ProviderID: "dm", ExternalRouteCode: "a|b", Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ms.UpsertTrip(ctx, memory.TripRow{RouteID: routeID, ProviderID: "dm", ExternalTripCode: "t1"}); err != nil {
		t.Fatal(err)
	}
	a := &App{store: ms}
	a.recordDemand(ctx, &model.Journey{Legs: []model.Leg{
		{TripID: "a|b|t1", RouteID: "a|b", ProviderID: "dm"},
	}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		stale, err := ms.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(stale) == 1 && stale[0].RouteCode == "a|b" && stale[0].ExternalCode == "t1" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipe-route tick never landed: %+v", stale)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

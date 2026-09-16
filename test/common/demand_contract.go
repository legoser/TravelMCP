package common

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/store"
)

// DemandSeeder — backend-specific seeding of one canonical route+trip.
// Returns the DB trip id plus the (provider, routeCode, externalTripCode)
// identity the store must resolve in RecordTripDemand.
type DemandSeeder interface {
	SeedDemandTrip(t *testing.T, tag string) (tripID int64, provider, routeCode, extCode string)
}

// AssertDemandContract — demand slice contract for every Store backend:
// unknown trips resolve to (false, nil); ticks accumulate; trips without
// sources are stale; a fresh source hides the trip until the cutoff passes
// it; ordering is highest demand first; limit cuts to the top.
func AssertDemandContract(t *testing.T, st store.Store, seeder DemandSeeder) {
	t.Helper()
	ctx := context.Background()
	rec, ok := st.(interface {
		RecordTripDemand(ctx context.Context, provider, routeCode, externalTripCode string) (bool, error)
		ListStaleDemandedTrips(ctx context.Context, cutoff time.Time, limit int) ([]store.StaleDemandRow, error)
	})
	if !ok {
		t.Fatal("store must implement demand recording")
	}
	src, ok := st.(interface {
		UpsertTripSource(ctx context.Context, s store.TripSourceRow) error
	})
	if !ok {
		t.Fatal("store must implement UpsertTripSource")
	}

	found, err := rec.RecordTripDemand(ctx, "ghost", "no-route", "no-trip")
	if err != nil || found {
		t.Fatalf("unknown trip: found=%v err=%v, want (false, nil)", found, err)
	}

	tripA, prov, route, ext := seeder.SeedDemandTrip(t, "a")
	for i := 0; i < 3; i++ {
		found, err = rec.RecordTripDemand(ctx, prov, route, ext)
		if err != nil || !found {
			t.Fatalf("tick %d: found=%v err=%v", i, found, err)
		}
	}
	stale, err := rec.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].RequestCount != 3 || !stale[0].OldestObservedAt.IsZero() {
		t.Fatalf("fresh demand must be stale with count 3 and zero observed_at: %+v", stale)
	}
	if stale[0].RouteCode != route || stale[0].ExternalCode != ext || stale[0].ProviderID != prov {
		t.Fatalf("identity mismatch: %+v", stale[0])
	}
	if stale[0].LastRequestedAt.IsZero() {
		t.Fatalf("last_requested_at must be set: %+v", stale[0])
	}

	if found, err = rec.RecordTripDemand(ctx, prov, route, ext); err != nil || !found {
		t.Fatalf("fourth tick: found=%v err=%v", found, err)
	}
	if err := src.UpsertTripSource(ctx, store.TripSourceRow{TripID: tripA, Source: prov}); err != nil {
		t.Fatal(err)
	}
	// Fresh source (observed now) hides trip A while the cutoff is an hour back.
	stale, err = rec.ListStaleDemandedTrips(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range stale {
		if r.TripID == tripA {
			t.Fatalf("freshly observed trip must be hidden: %+v", stale)
		}
	}
	// ...but a future cutoff passes it: stale again, observed_at filled.
	stale, err = rec.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].TripID != tripA || stale[0].OldestObservedAt.IsZero() {
		t.Fatalf("aged source must resurface with observed_at: %+v", stale)
	}

	_, prov2, route2, ext2 := seeder.SeedDemandTrip(t, "b")
	if _, err = rec.RecordTripDemand(ctx, prov2, route2, ext2); err != nil {
		t.Fatal(err)
	}
	stale, err = rec.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 2 || stale[0].RequestCount != 4 || stale[1].RequestCount != 1 {
		t.Fatalf("ordering must be demand-desc: %+v", stale)
	}
	stale, err = rec.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].RequestCount != 4 {
		t.Fatalf("limit must cut to top-1: %+v", stale)
	}

	// Batched ticks resolve in one call and skip unknowns.
	if batch, ok := st.(interface {
		RecordTripDemandBatch(ctx context.Context, ticks []store.TripDemandTick) (int, error)
	}); ok {
		n, err := batch.RecordTripDemandBatch(ctx, []store.TripDemandTick{
			{Provider: prov, RouteCode: route, ExternalTripCode: ext},
			{Provider: prov2, RouteCode: route2, ExternalTripCode: ext2},
			{Provider: "ghost", RouteCode: "no-route", ExternalTripCode: "no-trip"},
		})
		if err != nil || n != 2 {
			t.Fatalf("batch: n=%d err=%v, want 2", n, err)
		}
	} else {
		t.Fatal("store must implement RecordTripDemandBatch")
	}

	// Tombstoned trips stop accumulating demand and leave the stale input.
	if tomb, ok := st.(interface {
		TombstoneTrip(ctx context.Context, tripID int64) error
	}); ok {
		tripC, provC, routeC, extC := seeder.SeedDemandTrip(t, "tomb")
		if found, err := rec.RecordTripDemand(ctx, provC, routeC, extC); err != nil || !found {
			t.Fatalf("pre-tombstone tick: found=%v err=%v", found, err)
		}
		if err := tomb.TombstoneTrip(ctx, tripC); err != nil {
			t.Fatal(err)
		}
		if found, err := rec.RecordTripDemand(ctx, provC, routeC, extC); err != nil || found {
			t.Fatalf("tombstoned tick: found=%v err=%v, want (false, nil)", found, err)
		}
		stale, err := rec.ListStaleDemandedTrips(ctx, time.Now().Add(time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range stale {
			if r.TripID == tripC {
				t.Fatalf("tombstoned trip must leave stale input: %+v", stale)
			}
		}
	}
}

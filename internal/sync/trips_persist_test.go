package sync

import (
	"context"
	"testing"

	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

func seedAttachTerminals(t *testing.T, ms *memory.MemoryStore, terms []AttachTerminal) {
	t.Helper()
	ctx := context.Background()
	for _, term := range terms {
		lat, lon := 0.0, 0.0
		if term.Lat != nil {
			lat = *term.Lat
		}
		if term.Lon != nil {
			lon = *term.Lon
		}
		if _, err := ms.UpsertTerminal(ctx, store.TerminalRow{ID: term.ID, Lat: lat, Lon: lon}, map[string]string{"ru": term.Name}, term.Codes); err != nil {
			t.Fatalf("seed terminal %d: %v", term.ID, err)
		}
	}
}

func persistFixture(t *testing.T) (AttachReport, *memory.MemoryStore) {
	t.Helper()
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	rep, err := AttachTrips(context.Background(), baseInput(trips, terms))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	ms := memory.NewMemoryStore()
	seedAttachTerminals(t, ms, terms)
	if _, err := PersistAttachReport(context.Background(), ms, rep, "mintrans"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	return rep, ms
}

func TestPersistPromotedAndStaged(t *testing.T) {
	rep, ms := persistFixture(t)
	ctx := context.Background()
	for _, p := range rep.Promoted {
		routeID, ok := ms.FindRouteID(ctx, "mintrans", p.RouteNK)
		if !ok {
			t.Fatalf("route %s не найден", p.RouteNK)
		}
		tr, ok := ms.FindTrip(ctx, routeID, SplitTripNK(p.TripNK))
		if !ok {
			t.Fatalf("trip %s не найден", p.TripNK)
		}
		if tr.ValidTo != nil {
			t.Fatalf("trip %s сразу тумстоун: %v", p.TripNK, *tr.ValidTo)
		}
		if tr.DirectionID == nil {
			t.Fatalf("trip %s без direction_id", p.TripNK)
		}
		if tr.DurationS == nil || *tr.DurationS <= 0 {
			t.Fatalf("trip %s без duration_s", p.TripNK)
		}
		if tr.Period != p.WinnerPeriod {
			t.Fatalf("trip %s period=%q winner=%q", p.TripNK, tr.Period, p.WinnerPeriod)
		}
	}
	staged, err := ms.ListStagingTrips(ctx, "", 100)
	if err != nil {
		t.Fatalf("staging: %v", err)
	}
	if len(staged) != len(rep.Staged) {
		t.Fatalf("staging=%d want %d", len(staged), len(rep.Staged))
	}
	for _, s := range staged {
		if s.State == "" || s.MatchedStopTimes == "" {
			t.Fatalf("staging = %+v", s)
		}
	}
	reviews, err := ms.ListReviewQueue(ctx, 100)
	if err != nil {
		t.Fatalf("reviews: %v", err)
	}
	if len(reviews) != len(rep.Reviews) {
		t.Fatalf("reviews=%d want %d", len(reviews), len(rep.Reviews))
	}
}

func TestPersistIdempotentRerun(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	rep, err := AttachTrips(context.Background(), baseInput(trips, terms))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	ms := memory.NewMemoryStore()
	seedAttachTerminals(t, ms, terms)
	ctx := context.Background()
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans"); err != nil {
		t.Fatalf("persist1: %v", err)
	}
	staged1, _ := ms.ListStagingTrips(ctx, "", 100)
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans"); err != nil {
		t.Fatalf("persist2: %v", err)
	}
	staged2, _ := ms.ListStagingTrips(ctx, "", 100)
	if len(staged1) != len(staged2) {
		t.Fatalf("ресинк плодит staging: %d != %d", len(staged1), len(staged2))
	}
	for _, p := range rep.Promoted {
		routeID, _ := ms.FindRouteID(ctx, "mintrans", p.RouteNK)
		if _, ok := ms.FindTrip(ctx, routeID, SplitTripNK(p.TripNK)); !ok {
			t.Fatalf("trip %s потерян при ресинке", p.TripNK)
		}
	}
}

func TestPersistTombstoneAndResurrect(t *testing.T) {
	rep, ms := persistFixture(t)
	ctx := context.Background()
	p := rep.Promoted[0]
	routeID, _ := ms.FindRouteID(ctx, "mintrans", p.RouteNK)
	code := SplitTripNK(p.TripNK)
	tomb := AttachReport{Tombstoned: []TombstonedTrip{{RouteNK: p.RouteNK, TripNK: p.TripNK}}}
	if _, err := PersistAttachReport(ctx, ms, tomb, "mintrans"); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	tr, ok := ms.FindTrip(ctx, routeID, code)
	if !ok {
		t.Fatalf("trip пропал вместо tombstone")
	}
	if tr.ValidTo == nil {
		t.Fatal("tombstone не проставил valid_to")
	}
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans"); err != nil {
		t.Fatalf("repromote: %v", err)
	}
	tr, _ = ms.FindTrip(ctx, routeID, code)
	if tr.ValidTo != nil {
		t.Fatalf("воскрешение не сбросило valid_to: %v", *tr.ValidTo)
	}
}

func TestPersistOutboxAutopromote(t *testing.T) {
	trips := loadFlatTrips(t)
	full := indexFromFixture(t, trips)
	rep, err := AttachTrips(context.Background(), baseInput(trips, full[:len(full)-1]))
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Staged) == 0 {
		t.Fatal("нужен хотя бы один staged для автопромоушна")
	}
	ms := memory.NewMemoryStore()
	seedAttachTerminals(t, ms, full)
	ctx := context.Background()
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	stagedBefore, _ := ms.ListStagingTrips(ctx, "", 100)
	region := stagedBefore[0].Region
	if err := PublishTerminalCreated(ctx, ms, 999, region); err != nil {
		t.Fatalf("publish: %v", err)
	}
	n, err := ConsumeTerminalCreated(ctx, ms, func(r string) error {
		if r != region {
			t.Fatalf("region=%q want %q", r, region)
		}
		fullRep, err := AttachTrips(ctx, baseInput(trips, full))
		if err != nil {
			return err
		}
		_, err = PersistAttachReport(ctx, ms, fullRep, "mintrans")
		return err
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if n != 1 {
		t.Fatalf("consumed=%d want 1", n)
	}
	rest, _ := ms.ListOutbox(ctx, 100)
	if len(rest) != 0 {
		t.Fatalf("outbox не вычищен: %d", len(rest))
	}
}

func TestTripsAttachJobsRoundtrip(t *testing.T) {
	trips := loadFlatTrips(t)
	full := indexFromFixture(t, trips)
	ms := memory.NewMemoryStore()
	seedAttachTerminals(t, ms, full)
	ctx := context.Background()
	ids, err := EnqueueTripsAttachJobs(ctx, ms, []string{"54.22.078", "54.70.040"}, TripsAttachPayload{Source: "mintrans", Region: "54"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("jobs=%d want 2", len(ids))
	}
	run := func(ctx context.Context, route string) (AttachReport, error) {
		in := baseInput(trips, full)
		rep, err := AttachTrips(ctx, in)
		if err != nil {
			return rep, err
		}
		var promoted []PromotableTrip
		var staged []StagedTrip
		for _, p := range rep.Promoted {
			if p.RouteReg == route {
				promoted = append(promoted, p)
			}
		}
		for _, s := range rep.Staged {
			if s.RouteReg == route {
				staged = append(staged, s)
			}
		}
		rep.Promoted, rep.Staged = promoted, staged
		rep.Dead = nil
		rep.Reviews = nil
		rep.In = len(promoted) + len(staged)
		return rep, nil
	}
	var total PersistSummary
	for range ids {
		job, err := ms.ClaimNextJob(ctx)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		sum, err := HandleTripsAttachJob(ctx, ms, *job, run)
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		if err := ms.MarkJobDone(ctx, job.ID); err != nil {
			t.Fatalf("done: %v", err)
		}
		total.Trips += sum.Trips
		total.Staged += sum.Staged
		total.Routes += sum.Routes
	}
	if total.Trips == 0 {
		t.Fatalf("jobs ничего не записали: %+v", total)
	}
}

func TestCheckAttachBarrier(t *testing.T) {
	ok, waiting := CheckAttachBarrier(map[string]bool{"42": true, "54": true}, []string{"42", "54"})
	if !ok || len(waiting) != 0 {
		t.Fatalf("barrier = %v %v", ok, waiting)
	}
	ok, waiting = CheckAttachBarrier(map[string]bool{"42": true}, []string{"42", "54"})
	if ok || len(waiting) != 1 || waiting[0] != "54" {
		t.Fatalf("barrier = %v %v", ok, waiting)
	}
}

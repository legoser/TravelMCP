package sync

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

func seedSkeletonTerms(t *testing.T, ms *memory.MemoryStore, terms []AttachTerminal) {
	t.Helper()
	ctx := context.Background()
	runID, err := ms.CreateSyncRun(ctx, store.SyncRunRow{PlanID: "test", Kind: "skeleton"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	for _, term := range terms {
		if term.ID == 0 || term.Name == "" {
			continue
		}
		lat, lon := 0.0, 0.0
		if term.Lat != nil {
			lat = *term.Lat
		}
		if term.Lon != nil {
			lon = *term.Lon
		}
		if _, err := ms.UpsertTerminal(ctx, store.TerminalRow{ID: term.ID, Lat: lat, Lon: lon, EnrichmentStatus: "enriched"}, map[string]string{"ru": term.Name}, term.Codes); err != nil {
			t.Fatalf("seed terminal %d: %v", term.ID, err)
		}
		syncID := runID
		if err := ms.UpsertAttributeState(ctx, store.AttributeStateRow{
			EntityType: "terminal", EntityID: term.ID, Field: "geom",
			Value: "1,1", Source: "osm", Confidence: 0.9, Origin: "live", SyncRunID: &syncID,
		}); err != nil {
			t.Fatalf("seed attr %d: %v", term.ID, err)
		}
	}
}

func runCfg() TripsRunConfig {
	return TripsRunConfig{
		Source: "mintrans", TrustRouteNK: true,
		ChurnThreshold: 0.2, MaxSpeedKmh: 200,
		CoverageGate: 0, SoftScore: 0.4, Margin: 0.1,
		ParamsFor: attachParamsFor, ClassFor: nil,
		PlanID: "test", InputSHA: "test", Tag: "test",
	}
}

func TestRunTripsSyncFull(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	ms := memory.NewMemoryStore()
	seedSkeletonTerms(t, ms, terms)
	ctx := context.Background()
	runID, err := BeginTripsRun(ctx, ms, "plan", "sha", "test")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	sum, err := RunTripsSync(ctx, ms, ms, trips, runCfg())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !sum.GatePass || len(sum.Blocked) != 0 {
		t.Fatalf("gate = %v %v, want pass", sum.GatePass, sum.Blocked)
	}
	if sum.Routes != 4 {
		t.Fatalf("routes=%d want 4", sum.Routes)
	}
	if len(sum.Skipped) != 0 {
		t.Fatalf("skipped=%v want none", sum.Skipped)
	}
	if sum.Persist.Trips != 4 || sum.Persist.Staged != 2 {
		t.Fatalf("persist=%+v want trips=4 staged=2", sum.Persist)
	}
	if sum.FullTripRate != 0.5 {
		t.Fatalf("full_rate=%.3f want 0.5", sum.FullTripRate)
	}
	if len(sum.ByRoute) != 4 {
		t.Fatalf("by_route=%d want 4", len(sum.ByRoute))
	}
	tags, err := ms.GetTerminalTags(ctx, terms[0].ID)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	if tags["intercity"] != "1" {
		t.Fatalf("терминал сматченного рейса обязан нести tag intercity: %v", tags)
	}
	if err := FinishTripsRun(ctx, ms, runID, "done", sum); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func TestRunTripsSyncGateBlocks(t *testing.T) {
	trips := loadFlatTrips(t)
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	cfg := runCfg()
	cfg.CoverageGate = 0.8
	sum, err := RunTripsSync(ctx, ms, ms, trips, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sum.GatePass || len(sum.Blocked) == 0 {
		t.Fatalf("пустой скелет обязан блокировать gate: %+v", sum)
	}
	if len(sum.Skipped) == 0 {
		t.Fatal("заблокированные регионы обязаны пропускать маршруты")
	}
	if sum.Persist.Trips != 0 || sum.Persist.Staged != 0 {
		t.Fatalf("при блокировке записи быть не должно: %+v", sum.Persist)
	}
}

func TestRunTripsSyncWaitBarrier(t *testing.T) {
	trips := loadFlatTrips(t)
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	cfg := runCfg()
	cfg.CoverageGate = 0.8
	cfg.WaitForGate = 60 * time.Millisecond
	cfg.PollInterval = 10 * time.Millisecond
	start := time.Now()
	sum, err := RunTripsSync(ctx, ms, ms, trips, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !sum.Waited || time.Since(start) < 50*time.Millisecond {
		t.Fatal("барьер обязан ждать до таймаута gate")
	}
	if sum.GatePass || len(sum.Skipped) == 0 {
		t.Fatalf("после таймаута заблокированные регионы пропускаются: %+v", sum)
	}
}

func TestRunTripsSyncForce(t *testing.T) {
	trips := loadFlatTrips(t)
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	cfg := runCfg()
	cfg.CoverageGate = 0.8
	cfg.Force = true
	sum, err := RunTripsSync(ctx, ms, ms, trips, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !sum.Forced || len(sum.Skipped) != 0 {
		t.Fatalf("force обязан прикрепить всё: %+v", sum)
	}
	if sum.Persist.Staged == 0 {
		t.Fatalf("без скелета всё обязано уйти в staging: %+v", sum.Persist)
	}
}

func TestRunTripsSyncDryRun(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	ms := memory.NewMemoryStore()
	seedSkeletonTerms(t, ms, terms)
	ctx := context.Background()
	cfg := runCfg()
	cfg.DryRun = true
	sum, err := RunTripsSync(ctx, ms, ms, trips, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sum.Persist.Trips != 0 {
		t.Fatalf("dry-run обязан ничего не писать: %+v", sum.Persist)
	}
	if len(sum.ByRoute) != 4 || sum.FullTripRate != 0.5 {
		t.Fatalf("dry-run обязан посчитать сводку: %+v", sum)
	}
	if _, ok := ms.FindRouteID(ctx, "mintrans", "54.22.078"); ok {
		t.Fatal("dry-run записал route в стор")
	}
}

func TestSkeletonRowToAttachTerminals(t *testing.T) {
	rows := []store.SkeletonTerminalRow{{
		ID: 7, NameRu: "Вокзал", Lat: 55.0, Lon: 86.0,
		Settlement: "кемерово", EnrichmentStatus: "enriched",
		Identifiers: []model.AdaptedIdentifier{{System: "osm", CodeType: "osm_id", Code: "n1"}},
	}}
	terms := SkeletonRowToAttachTerminals(rows)
	if len(terms) != 1 {
		t.Fatalf("terms=%d want 1", len(terms))
	}
	tm := terms[0]
	if tm.ID != 7 || tm.Name != "Вокзал" || tm.Source != "osm" || !tm.GeomFinalized {
		t.Fatalf("маппинг skeleton→attach неверен: %+v", tm)
	}
	if tm.Lat == nil || *tm.Lat != 55.0 || tm.Settlement != "кемерово" {
		t.Fatalf("гео/поселение потеряны: %+v", tm)
	}
	idOnly := []store.SkeletonTerminalRow{{
		ID: 8, NameRu: "Платформа", Lat: 55.0, Lon: 86.0, EnrichmentStatus: "identity_only",
	}}
	if tm2 := SkeletonRowToAttachTerminals(idOnly)[0]; tm2.GeomFinalized {
		t.Fatal("identity_only обязан давать GeomFinalized=false (soft-валидатор, не dead)")
	}
}

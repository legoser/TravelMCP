package sync

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
	memstore "travelmcp/internal/store/memory"
)

func promoteOneSkeleton(t *testing.T, ctx context.Context, ms *memstore.MemoryStore, runID int64) {
	t.Helper()
	rec := model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: "Кемерово автовокзал",
		Source: "osm",
		Identifiers: []model.AdaptedIdentifier{
			{System: "osm", CodeType: "osm_id", Code: "10"},
		},
		Extra: map[string]string{"region": "kuzbass", "transport_type": "bus", "settlement": "Кемерово"},
	}
	la, lo := 55.3416, 86.061
	rec.Lat, rec.Lon = &la, &lo
	chunk := SkeletonChunk{
		Key:   "test",
		Canon: []skeleton.JoinedRecord{{Record: rec, Score: 0.9, Enrichment: skeleton.Enriched}},
	}
	if _, err := PromoteSkeletonChunk(ctx, ms, runID, chunk); err != nil {
		t.Fatal(err)
	}
}

func seedLegacy(t *testing.T, ctx context.Context, ms *memstore.MemoryStore, name string, lat, lon float64) int64 {
	t.Helper()
	id, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: lat, Lon: lon}, map[string]string{"ru": name}, []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: "42-001"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: id, Source: "mintrans", Confidence: 0.5, ObservedAt: time.Now(), Channel: model.ChannelLocalFile}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMatchLegacyGeomOverlap(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	skelRun, err := BeginSkeletonRun(ctx, ms, "plan", "sha", "test")
	if err != nil {
		t.Fatal(err)
	}
	promoteOneSkeleton(t, ctx, ms, skelRun)
	legID := seedLegacy(t, ctx, ms, "Кемерово АВ", 55.3416, 86.061)
	phase0, err := BeginPhase0Run(ctx, ms, "plan", "sha", "test")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := MatchLegacyTerminals(ctx, ms, phase0, 0.6, skeleton.DefaultJoinConfig())
	if err != nil {
		t.Fatal(err)
	}
	if sum.In != 1 || sum.Matched != 1 || sum.Review != 0 {
		t.Fatalf("ожидался матч 1/1/0, получено %+v", sum)
	}
	states, err := ms.ListAttributeStates(ctx, "terminal", legID)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) < 2 {
		t.Fatalf("конкурс обязан писать attribute_state, получено %+v", states)
	}
	sum2, err := MatchLegacyTerminals(ctx, ms, phase0, 0.6, skeleton.DefaultJoinConfig())
	if err != nil {
		t.Fatal(err)
	}
	if sum2.Updated != 0 {
		t.Fatalf("повторный прогон обязан сходиться без обновлений: %+v", sum2)
	}
}

func TestMatchLegacyUnmatchedToReview(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	skelRun, err := BeginSkeletonRun(ctx, ms, "plan", "sha", "test")
	if err != nil {
		t.Fatal(err)
	}
	promoteOneSkeleton(t, ctx, ms, skelRun)
	legID := seedLegacy(t, ctx, ms, "ОП Заюлино", 60.0, 100.0)
	phase0, _ := BeginPhase0Run(ctx, ms, "plan", "sha", "test")
	sum, err := MatchLegacyTerminals(ctx, ms, phase0, 0.6, skeleton.DefaultJoinConfig())
	if err != nil {
		t.Fatal(err)
	}
	if sum.In != 1 || sum.Matched != 0 || sum.Review != 1 {
		t.Fatalf("далёкий legacy — в review: %+v", sum)
	}
	rq, err := ms.ListReviewQueue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rq {
		if r.EntityID == legID && r.Reason == "legacy_unmatched" && r.Fingerprint != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("review_queue{legacy_unmatched} с fingerprint: %+v", rq)
	}
}

func TestMatchLegacyManualSticky(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	skelRun, err := BeginSkeletonRun(ctx, ms, "plan", "sha", "test")
	if err != nil {
		t.Fatal(err)
	}
	promoteOneSkeleton(t, ctx, ms, skelRun)
	legID := seedLegacy(t, ctx, ms, "Кемерово автовокзал", 55.345, 86.065)
	actor := int64(7)
	phase0, _ := BeginPhase0Run(ctx, ms, "plan", "sha", "test")
	if err := ms.UpsertAttributeState(ctx, store.AttributeStateRow{EntityType: "terminal", EntityID: legID, Field: "geom", Value: "55.345,86.065", Source: "manual", Confidence: 0.1, Origin: "live", ActorID: &actor, SyncRunID: &phase0}); err != nil {
		t.Fatal(err)
	}
	sum, err := MatchLegacyTerminals(ctx, ms, phase0, 0.6, skeleton.DefaultJoinConfig())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Matched != 1 {
		t.Fatalf("матч обязан состояться: %+v", sum)
	}
	if sum.Updated != 0 {
		t.Fatalf("ручное поле трогать нельзя: %+v", sum)
	}
	got, err := ms.GetTerminal(ctx, legID)
	if err != nil {
		t.Fatal(err)
	}
	if got["lat"] != 55.345 {
		t.Fatalf("ручные координаты обязаны уцелеть: %+v", got)
	}
}

func TestMeasureCoverage(t *testing.T) {
	skels := []store.SkeletonTerminalRow{
		{ID: 1, NameRu: "Кемерово автовокзал", Lat: 55.34, Lon: 86.06, Settlement: "Кемерово"},
		{ID: 2, NameRu: "Топки автостанция", Lat: 55.27, Lon: 85.61, Settlement: "Топки"},
	}
	stops := []RegistryStop{
		{Name: "Кемеровский АВ", Region: "42", Settlement: "Кемерово"},
		{Name: "ОП г. Топки", Region: "42", Settlement: "Топки"},
		{Name: "ОП Заюлино", Region: "42", Settlement: "Заюлино"},
	}
	cov := MeasureCoverage(stops, skels, 0.4, skeleton.DefaultJoinConfig())
	if len(cov) != 1 || cov[0].Total != 3 {
		t.Fatalf("покрытие: %+v", cov)
	}
	if cov[0].Matched != 2 {
		t.Fatalf("2 из 3 обязаны сматчиться мягким порогом: %+v", cov)
	}
	ok, blocked := skeleton.GatePass(ToSkeletonCoverages(cov), 0.5)
	if !ok || len(blocked) != 0 {
		t.Fatalf("gate 0.5 при 2/3 обязан проходиться: %v %v", ok, blocked)
	}
	if ok2, _ := skeleton.GatePass(ToSkeletonCoverages(cov), 0.9); ok2 {
		t.Fatal("gate 0.9 при 2/3 обязан блокировать")
	}
}

func TestChunkEnsureCompleteSkip(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	runID, err := BeginSkeletonRun(ctx, ms, "plan-a", "sha", "t")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ms.EnsureSyncChunk(ctx, runID, "terminal", "k1")
	if err != nil || id == 0 {
		t.Fatalf("ensure: %v %d", err, id)
	}
	id2, err := ms.EnsureSyncChunk(ctx, runID, "terminal", "k1")
	if err != nil || id2 != id {
		t.Fatalf("ensure идемпотентен: %v %d %d", err, id, id2)
	}
	if err := ms.CompleteSyncChunk(ctx, id, "plan-a", "done", ""); err != nil {
		t.Fatal(err)
	}
	got, ok := ms.GetSyncChunk(ctx, runID, "terminal", "k1")
	if !ok || !ChunkFresh(got.PlanIDDone, "plan-a") {
		t.Fatalf("чанк обязан быть свежим: %+v %v", got, ok)
	}
	if ChunkFresh(got.PlanIDDone, "plan-b") {
		t.Fatal("смена plan_id обязана инвалидировать чанк")
	}
}

package sync

import (
	"context"
	"testing"

	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	"travelmcp/internal/store"
	memstore "travelmcp/internal/store/memory"
)

func skelRec(name, region, transport, code string, lat, lon float64, extra map[string]string) model.AdaptedRecord {
	r := model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: name,
		Source: "osm",
		Identifiers: []model.AdaptedIdentifier{
			{System: "osm", CodeType: "osm_id", Code: code},
		},
		Extra: map[string]string{"region": region, "transport_type": transport, "settlement": region},
	}
	if lat != 0 || lon != 0 {
		la, lo := lat, lon
		r.Lat, r.Lon = &la, &lo
	}
	for k, v := range extra {
		r.Extra[k] = v
	}
	return r
}

func TestChunkSkeletonDeterministic(t *testing.T) {
	outcome := skeleton.JoinOutcome{
		Canon: []skeleton.JoinedRecord{
			{Record: skelRec("Б", "kuzbass", "bus", "2", 55.0, 86.0, nil), Score: 0.9, Enrichment: skeleton.Enriched},
			{Record: skelRec("А", "kuzbass", "bus", "1", 55.1, 86.1, nil), Score: 0.8, Enrichment: skeleton.IdentityOnly},
			{Record: skelRec("В", "nsk", "rail", "3", 55.0, 83.0, nil), Score: 0.7, Enrichment: skeleton.IdentityOnly},
		},
		Unverified: []model.AdaptedRecord{
			{Kind: model.AdaptedTerminal, NameRu: "Только Яндекс", Source: "yandex", Extra: map[string]string{"region": "kuzbass"}},
		},
	}
	a := ChunkSkeleton(outcome, 2)
	b := ChunkSkeleton(outcome, 2)
	if len(a) != 3 {
		t.Fatalf("чанк size=2: 3 canon + 1 unverified = 3 чанка, получено %d", len(a))
	}
	for i := range a {
		if a[i].Key != b[i].Key {
			t.Fatalf("чанки обязаны быть детерминированы: %q != %q", a[i].Key, b[i].Key)
		}
	}
	if a[0].Canon[0].Record.NameRu != "А" {
		t.Fatalf("сортировка регион→транспорт→код: первый обязан быть А, получен %q", a[0].Canon[0].Record.NameRu)
	}
	for _, c := range a {
		if len(c.Canon)+len(c.Unverified) > 2 {
			t.Fatalf("чанк больше заданного размера: %+v", c)
		}
	}
}

func TestPromoteSkeletonChunkMemory(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	runID, err := BeginSkeletonRun(ctx, ms, "plan-test", "sha-test", "skeleton-test")
	if err != nil {
		t.Fatal(err)
	}
	if runID == 0 {
		t.Fatal("runID обязан быть ненулевым")
	}
	enriched := skelRec("Кемерово автовокзал", "kuzbass", "bus", "10", 55.3416, 86.061, map[string]string{"yandex_title": "Автовокзал Кемерово"})
	identity := skelRec("Тайга станция", "kuzbass", "rail", "11", 56.05, 85.62, nil)
	nogeom := skelRec("Без координат", "kuzbass", "bus", "12", 0, 0, nil)
	yandexOnly := model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: "Только Яндекс остановка",
		Source: "yandex",
		Identifiers: []model.AdaptedIdentifier{
			{System: "yandex", CodeType: "yandex_code", Code: "s9999"},
		},
		Extra: map[string]string{"region": "kuzbass", "settlement": "Кемерово", "transport_type": "bus"},
	}
	chunk := SkeletonChunk{
		Key: "test-chunk",
		Canon: []skeleton.JoinedRecord{
			{Record: enriched, Score: 0.9, Enrichment: skeleton.Enriched},
			{Record: identity, Score: 0.5, Enrichment: skeleton.IdentityOnly},
			{Record: nogeom, Score: 0.1, Enrichment: skeleton.IdentityOnly},
		},
		Unverified: []model.AdaptedRecord{yandexOnly},
	}
	sum, err := PromoteSkeletonChunk(ctx, ms, runID, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if sum.In != 4 || sum.Written != 2 || sum.Review != 2 || sum.Enriched != 1 {
		t.Fatalf("баланс чанка: in=4 written=2 review=2 enriched=1, получено %+v", sum)
	}
	list, total, err := ms.ListTerminalsFiltered(ctx, 10, 0, "name", "asc", "Кемерово")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0]["name"] != "Кемерово автовокзал" {
		t.Fatalf("терминал обязан читаться по имени: total=%d %+v", total, list)
	}
	rq, err := ms.ListReviewQueue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rq) != 2 {
		t.Fatalf("review_queue: nogeom + yandex-only = 2, получено %+v", rq)
	}
	seen := map[string]bool{}
	for _, r := range rq {
		seen[r.Reason] = true
		if r.Fingerprint == "" {
			t.Fatalf("fingerprint обязан быть по внешней идентичности: %+v", r)
		}
		if r.EntityID >= 0 {
			t.Fatalf("доканонический review — синтетический отрицательный ID: %+v", r)
		}
	}
	if !seen["low_confidence"] || !seen["skeleton_unverified"] {
		t.Fatalf("причины review: %+v", seen)
	}
	rs := RunSummary{In: sum.In, Written: sum.Written, Review: sum.Review, Enriched: sum.Enriched, Chunks: 1}
	if err := FinishSkeletonRun(ctx, ms, runID, "done", rs); err != nil {
		t.Fatal(err)
	}
	if err := CheckRunBalance(RunSummary{In: 4, Written: 2, Review: 1}); err == nil {
		t.Fatal("несходимость обязана быть fail-loud")
	}
}

func TestYandexNameAddsContext(t *testing.T) {
	cases := []struct {
		yandex, osm string
		want        bool
	}{
		{"Кемерово, автовокзал", "Автовокзал", true},
		{"Кемерово, ж/д вокзал", "Ж/Д вокзал", true},
		{"Барнаул", "Барнаул", false},
		{"Автовокзал", "Кемерово, автовокзал", false},
		{"Томск, автовокзал", "Томская станция", false},
		{"", "Автовокзал", false},
		{"Кемерово, автовокзал", "", false},
	}
	for _, c := range cases {
		if got := yandexNameAddsContext(c.yandex, c.osm); got != c.want {
			t.Errorf("yandexNameAddsContext(%q, %q) = %v, want %v", c.yandex, c.osm, got, c.want)
		}
	}
}

// issue #8/#14: терминал, одобренный оператором, не должен возвращаться в
// модерацию при следующем прогоне skeleton-sync. Сценарий: промоут →
// оператор одобрил (is_locked, review resolved) → повторный промоут той
// же записи (тот же osm-код) → конфликт sticky, open review пуст.
func TestPromoteSkeletonChunkStickyAfterApprove(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	runID, err := BeginSkeletonRun(ctx, ms, "plan-sticky", "sha-sticky", "skeleton-sticky")
	if err != nil {
		t.Fatal(err)
	}
	rec := skelRec("Юрга, автовокзал", "kuzbass", "bus", "777", 55.72, 84.31, nil)
	chunk := SkeletonChunk{Key: "sticky-chunk", Canon: []skeleton.JoinedRecord{
		{Record: rec, Score: 0.9, Enrichment: skeleton.Enriched},
	}}
	if _, err := PromoteSkeletonChunk(ctx, ms, runID, chunk); err != nil {
		t.Fatal(err)
	}
	list, total, err := ms.ListTerminalsFiltered(ctx, 10, 0, "name", "asc", "Юрга")
	if err != nil || total != 1 {
		t.Fatalf("после промоута терминал обязан существовать: total=%d err=%v", total, err)
	}
	tid, _ := list[0]["id"].(int64)

	// оператор одобряет: approve-путь handleReviewResolve
	tr := store.TerminalRow{ID: tid, Lat: 55.72, Lon: 84.31, IsLocked: true}
	if _, err := ms.UpsertTerminal(ctx, tr, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := ms.ResolveReviewQueue(ctx, "terminal", tid, "conflicts_with_confirmed", "resolved"); err != nil {
		t.Fatal(err)
	}

	// следующий skeleton-sync: та же запись (тот же внешний код)
	runID2, err := BeginSkeletonRun(ctx, ms, "plan-sticky2", "sha-sticky2", "skeleton-sticky2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteSkeletonChunk(ctx, ms, runID2, chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.UpsertTerminal(ctx, tr, nil, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := ms.ListReviewQueue(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.EntityType == "terminal" && r.EntityID == tid {
			t.Fatalf("одобренный терминал не должен возвращаться в open review: %+v", r)
		}
	}
}

func TestPromoteSkeletonChunkYandexPrimaryName(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	runID, err := BeginSkeletonRun(ctx, ms, "plan-name", "sha-name", "skeleton-name")
	if err != nil {
		t.Fatal(err)
	}
	rec := skelRec("Автовокзал", "Кемерово", "bus", "42", 55.3416, 86.061,
		map[string]string{"yandex_title": "Кемерово, автовокзал", "settlement": "Кемерово"})
	chunk := SkeletonChunk{Key: "name-chunk", Canon: []skeleton.JoinedRecord{
		{Record: rec, Score: 0.9, Enrichment: skeleton.Enriched},
	}}
	if _, err := PromoteSkeletonChunk(ctx, ms, runID, chunk); err != nil {
		t.Fatal(err)
	}
	list, total, err := ms.ListTerminalsFiltered(ctx, 10, 0, "name", "asc", "Кемерово, автовокзал")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0]["name"] != "Кемерово, автовокзал" {
		t.Fatalf("primary-имя должно брать yandex-title с городом: total=%d %+v", total, list)
	}
}

func TestPromoteSkeletonChunkYandexOnlyWithCoords(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	runID, err := BeginSkeletonRun(ctx, ms, "plan-test", "sha-test", "skeleton-test")
	if err != nil {
		t.Fatal(err)
	}

	yandexOnlyWithCoords := model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: "Только Яндекс с координатами",
		Source: "yandex",
		Identifiers: []model.AdaptedIdentifier{
			{System: "yandex", CodeType: "yandex_code", Code: "s5678"},
		},
		Extra: map[string]string{"region": "kuzbass", "transport_type": "bus"},
	}
	lat, lon := 55.5, 86.5
	yandexOnlyWithCoords.Lat = &lat
	yandexOnlyWithCoords.Lon = &lon

	yandexOnlyNoCoords := model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: "Только Яндекс без координат",
		Source: "yandex",
		Identifiers: []model.AdaptedIdentifier{
			{System: "yandex", CodeType: "yandex_code", Code: "s9998"},
		},
		Extra: map[string]string{"region": "kuzbass", "transport_type": "bus"},
	}

	chunk := SkeletonChunk{
		Key:        "yandex-chunk",
		Unverified: []model.AdaptedRecord{yandexOnlyWithCoords, yandexOnlyNoCoords},
	}

	sum, err := PromoteSkeletonChunk(ctx, ms, runID, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if sum.In != 2 {
		t.Fatalf("in=2, got %d", sum.In)
	}
	if sum.Written != 1 {
		t.Fatalf("yandex-only with coords должен попасть в canon: written=1, got %d", sum.Written)
	}
	if sum.Review != 1 {
		t.Fatalf("yandex-only without coords → review: review=1, got %d", sum.Review)
	}

	list, total, err := ms.ListTerminalsFiltered(ctx, 10, 0, "name", "asc", "Только Яндекс с координатами")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0]["name"] != "Только Яндекс с координатами" {
		t.Fatalf("терминал обязан промоунтиться в канон: total=%d", total)
	}

	rq, err := ms.ListReviewQueue(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rq) != 1 {
		t.Fatalf("только 1 запись в review_queue (без координат), получено %d", len(rq))
	}
	if rq[0].Reason != "skeleton_unverified" {
		t.Fatalf("причина review=skeleton_unverified, получено %q", rq[0].Reason)
	}
}

func TestPromoteSkeletonChunkDedupByCode(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	runID, err := BeginSkeletonRun(ctx, ms, "plan-dedup", "sha-dedup", "dedup-test")
	if err != nil {
		t.Fatal(err)
	}
	rec := model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: "Томск, автовокзал",
		Source: "yandex",
		Identifiers: []model.AdaptedIdentifier{
			{System: "yandex", CodeType: "yandex_code", Code: "s9623436"},
		},
		Extra: map[string]string{"region": "tomsk", "transport_type": "bus"},
	}
	lat, lon := 56.4612, 84.9912
	rec.Lat, rec.Lon = &lat, &lon

	chunk := SkeletonChunk{Key: "dedup-chunk", Unverified: []model.AdaptedRecord{rec}}
	if _, err := PromoteSkeletonChunk(ctx, ms, runID, chunk); err != nil {
		t.Fatal(err)
	}
	id1, ok := ms.ListTerminalIDByCode(ctx, "yandex", "s9623436")
	if !ok {
		t.Fatal("после первого прогона код обязан быть привязан")
	}
	// Повторный прогон той же записи: промоут обязан сматчиться по коду,
	// а не создать дубликат (баг Кузбасс-пилота: дубль при втором прогоне).
	if _, err := PromoteSkeletonChunk(ctx, ms, runID, chunk); err != nil {
		t.Fatal(err)
	}
	id2, ok := ms.ListTerminalIDByCode(ctx, "yandex", "s9623436")
	if !ok || id2 != id1 {
		t.Fatalf("повторный прогон обязан переиспользовать терминал %d, получен %d (ok=%v)", id1, id2, ok)
	}
	list, total, err := ms.ListTerminalsFiltered(ctx, 10, 0, "name", "asc", "Томск, автовокзал")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("дубликатов быть не должно: total=%d", total)
	}
	if len(list) > 0 && list[0]["id"].(int64) != id1 {
		t.Fatalf("канонический id=%d, в списке %v", id1, list[0]["id"])
	}
}

func TestSyntheticReviewIDStableNegative(t *testing.T) {
	a := syntheticReviewID("terminal", "skeleton_unverified", "yandex", "s9999")
	b := syntheticReviewID("terminal", "skeleton_unverified", "yandex", "s9999")
	if a != b || a >= 0 {
		t.Fatalf("ID обязан быть стабильным отрицательным, получено %d/%d", a, b)
	}
	if c := syntheticReviewID("terminal", "skeleton_unverified", "yandex", "s1000"); c == a {
		t.Fatal("разные внешние коды обязаны давать разные ID")
	}
}

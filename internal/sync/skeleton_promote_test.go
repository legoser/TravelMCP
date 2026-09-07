package sync

import (
	"context"
	"testing"

	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
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

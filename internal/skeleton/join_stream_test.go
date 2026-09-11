package skeleton

import (
	"testing"

	"travelmcp/internal/model"
)

func TestJoinPagerEquivalence(t *testing.T) {
	cfg := DefaultJoinConfig()
	osm := []model.AdaptedRecord{
		rec("Кемерово автовокзал", 55.3416, 86.061, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "1"}),
		rec("Тайга станция", 56.05, 85.62, map[string]string{"transport_type": "rail", "settlement": "Тайга"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "2"}),
		rec("Без пары OSM", 55.5, 86.5, map[string]string{"transport_type": "bus"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "3"}),
	}
	yandex := []model.AdaptedRecord{
		rec("Кемерово автовокзал", 55.3417, 86.0612, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s1001"}),
		rec("Только Яндекс остановка", 55.0, 86.0, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s9999"}),
		rec("Тайга станция", 56.05, 85.62, map[string]string{"transport_type": "rail", "settlement": "Тайга"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s1002"}),
	}

	single := Join(osm, yandex, cfg)

	// пагинация по 1: 3 страницы
	pager := NewJoinPager(osm, yandex, cfg, 1)
	var paginated JoinOutcome
	for {
		page, ok := pager.NextPage()
		if !ok {
			break
		}
		paginated.Canon = append(paginated.Canon, page.Canon...)
		paginated.Unverified = append(paginated.Unverified, page.Unverified...)
		paginated.DuplicateAmbiguous = append(paginated.DuplicateAmbiguous, page.DuplicateAmbiguous...)
	}
	paginated.Unverified = append(paginated.Unverified, pager.FlushUnverified()...)

	// verified-пары совпадают
	singleVerified := map[string]bool{}
	for _, c := range single.Canon {
		if c.Enrichment == Enriched {
			singleVerified[c.Record.PrimaryCode()] = true
		}
	}
	paginatedVerified := map[string]bool{}
	for _, c := range paginated.Canon {
		if c.Enrichment == Enriched {
			paginatedVerified[c.Record.PrimaryCode()] = true
		}
	}
	if len(singleVerified) != len(paginatedVerified) {
		t.Fatalf("verified-пары: single=%v paginated=%v", singleVerified, paginatedVerified)
	}
	for code := range singleVerified {
		if !paginatedVerified[code] {
			t.Fatalf("verified-пара %s потеряна в пагинации", code)
		}
	}
	if !paginatedVerified["1"] || !paginatedVerified["2"] {
		t.Fatalf("Кемерово (1) и Тайга (2) обязаны быть verified: %v", paginatedVerified)
	}

	// unverified совпадают
	singleUnverified := map[string]bool{}
	for _, u := range single.Unverified {
		singleUnverified[u.PrimaryCode()] = true
	}
	paginatedUnverified := map[string]bool{}
	for _, u := range paginated.Unverified {
		paginatedUnverified[u.PrimaryCode()] = true
	}
	if len(singleUnverified) != len(paginatedUnverified) {
		t.Fatalf("unverified: single=%v paginated=%v", singleUnverified, paginatedUnverified)
	}
	for code := range singleUnverified {
		if !paginatedUnverified[code] {
			t.Fatalf("unverified %s потерян в пагинации", code)
		}
	}
	if !paginatedUnverified["s9999"] {
		t.Fatal("Только Яндекс обязан уйти в unverified")
	}

	// общий размер канона: single Canon (verified+identity) vs paginated Canon
	if len(single.Canon) != len(paginated.Canon) {
		t.Fatalf("Canon размер: single=%d paginated=%d", len(single.Canon), len(paginated.Canon))
	}
}

func TestJoinPagerAcrossCellBoundary(t *testing.T) {
	// OSM в ячейке (110,172), Yandex на границе — матчинг обязан находить
	// пару через границу ячейки (соседи ±1)
	cfg := DefaultJoinConfig()
	osm := []model.AdaptedRecord{
		rec("Пограничная пара", 55.4999, 86.0, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "100"}),
	}
	yandex := []model.AdaptedRecord{
		rec("Пограничная пара", 55.5001, 86.0, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s200"}),
	}
	pager := NewJoinPager(osm, yandex, cfg, 100)
	page, ok := pager.NextPage()
	if !ok || len(page.Canon) != 1 || page.Canon[0].Enrichment != Enriched {
		t.Fatalf("пара через границу ячейки обязана матчиться: ok=%v canon=%d", ok, len(page.Canon))
	}
	if len(pager.FlushUnverified()) != 0 {
		t.Fatal("unverified обязан быть пуст")
	}
}

func TestJoinPagerNoCoordsFallback(t *testing.T) {
	// OSM без координат ищет пару по всем свободным Yandex (name-only),
	// но hardGuard без гео не даёт verified — IdentityOnly-канон
	cfg := DefaultJoinConfig()
	osm := []model.AdaptedRecord{
		rec("Именной терминал", 0, 0, map[string]string{"settlement": "Новосибирск"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "300"}),
	}
	yandex := []model.AdaptedRecord{
		rec("Именной терминал", 55.03, 82.92, map[string]string{"transport_type": "bus", "settlement": "Бердск"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s300"}),
	}
	pager := NewJoinPager(osm, yandex, cfg, 100)
	page, ok := pager.NextPage()
	if !ok || len(page.Canon) != 1 || page.Canon[0].Enrichment != IdentityOnly {
		t.Fatalf("OSM без координат — IdentityOnly-канон: ok=%v %+v", ok, page.Canon)
	}
	unv := pager.FlushUnverified()
	if len(unv) != 1 || unv[0].PrimaryCode() != "s300" {
		t.Fatalf("Yandex без verified-пары — unverified: %v", unv)
	}
}

func TestJoinPagerEmptyOSM(t *testing.T) {
	// Yandex-only скелет: пустой OSM → все Яндекс-записи обязаны уйти в
	// Unverified (FlushUnverified), без паник и пропусков
	cfg := DefaultJoinConfig()
	yandex := []model.AdaptedRecord{
		rec("Кемерово, автовокзал", 55.35, 86.08, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s9623379"}),
		rec("Кемерово-Пасс.", 55.36, 86.07, map[string]string{"transport_type": "rail", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s2028001"}),
	}
	pager := NewJoinPager(nil, yandex, cfg, 100)
	if _, ok := pager.NextPage(); ok {
		t.Fatal("пустой OSM — страниц быть не должно")
	}
	unv := pager.FlushUnverified()
	if len(unv) != 2 {
		t.Fatalf("все Яндекс-записи обязаны быть unverified: %d", len(unv))
	}
	for i, want := range []string{"s9623379", "s2028001"} {
		if unv[i].PrimaryCode() != want {
			t.Fatalf("unverified[%d]: got %s want %s", i, unv[i].PrimaryCode(), want)
		}
	}
}

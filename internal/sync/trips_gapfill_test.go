package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

// dumpFixture — минимальный дамп станций Яндекса: терминальная станция
// Барнаула (автовокзал, есть в реестровых рейсах как op:22:22165) и
// городская остановка (bus_stop — создаваться не должна).
func dumpFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "yandex.json")
	data := `{"countries":[{"title":"Россия","regions":[{"title":"Алтайский край","settlements":[
{"title":"Барнаул","stations":[
{"title":"Барнаул, автовокзал","longitude":83.7586,"latitude":53.3517,"transport_type":"bus","station_type":"bus_station","codes":{"yandex_code":"c22165"}},
{"title":"Остановка Пушкина","longitude":83.77,"latitude":53.36,"transport_type":"bus","station_type":"bus_stop","codes":{"yandex_code":"c_bus_stop_1"}}]},
{"title":"Село Без Кода","stations":[
{"title":"Безымянная станция без координат","transport_type":"bus","station_type":"station","codes":{"yandex_code":"c_nocoords"}}]}
]}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// endpointTrips — рейс Новосибрск→Барнаул: конечный стоп Барнаула несёт
// yandex_code c22165, которого нет в каноническом пуле.
func endpointTrips() []model.FlatTrip {
	return []model.FlatTrip{{
		RouteNK: "r-test", RouteReg: "r-test", Direction: "forward", ServiceID: 1, Run: 1,
		Stops: []model.FlatStop{
			{StopID: "nsk", Name: "АВ г. Новосибирск", Region: "54", Lat: ptrF(55.04), Lon: ptrF(82.92),
				ArrMin: ptrI(480), DepMin: ptrI(480), Codes: []model.AdaptedIdentifier{{System: "gov-registry", Code: "54099"}}},
			{StopID: "barn", Name: "АВ г. Барнаул", Region: "22", Lat: ptrF(53.35), Lon: ptrF(83.75),
				ArrMin: ptrI(1020), DepMin: ptrI(1020), Codes: []model.AdaptedIdentifier{{System: "yandex", CodeType: "yandex_code", Code: "c22165"}}},
		},
	}}
}

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }

func seedCanonTerminal(t *testing.T, ms *memory.MemoryStore) AttachTerminal {
	t.Helper()
	ctx := context.Background()
	runID, err := ms.CreateSyncRun(ctx, store.SyncRunRow{PlanID: "test", Kind: "skeleton"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 55.04, Lon: 82.92, EnrichmentStatus: "enriched"}, map[string]string{"ru": "АВ г. Новосибирск"}, []model.AdaptedIdentifier{{System: "gov-registry", CodeType: "registry", Code: "54099"}})
	if err != nil {
		t.Fatal(err)
	}
	syncID := runID
	if err := ms.UpsertAttributeState(ctx, store.AttributeStateRow{EntityType: "terminal", EntityID: id, Field: "geom", Value: "55.04,82.92", Source: "osm", Confidence: 0.9, Origin: "live", SyncRunID: &syncID}); err != nil {
		t.Fatal(err)
	}
	return AttachTerminal{ID: id, Name: "АВ г. Новосибирск", Lat: ptrF(55.04), Lon: ptrF(82.92), Settlement: "Новосибирск", Transport: "bus", Source: "osm", GeomFinalized: true, Codes: []model.AdaptedIdentifier{{System: "gov-registry", Code: "54099"}}}
}

func TestGapFillCreatesEndpointTerminal(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	nsk := seedCanonTerminal(t, ms)
	terms := []AttachTerminal{nsk}

	sum, terms2, err := GapFillEndpoints(ctx, ms, terms, endpointTrips(), GapFillConfig{DumpPath: dumpFixture(t)})
	if err != nil {
		t.Fatalf("gapfill: %v", err)
	}
	if sum.MissingEndpoints == 0 {
		t.Fatalf("барнаульский конец обязан быть missing (канон его не знает)")
	}
	if sum.Added != 1 {
		t.Fatalf("added=%d want 1 (сумма=%+v)", sum.Added, sum)
	}
	if sum.SkippedNoDump != 0 {
		t.Fatalf("skipped_no_dump=%d want 0", sum.SkippedNoDump)
	}
	if len(terms2) != len(terms)+1 {
		t.Fatalf("пул терминалов должен пополниться")
	}
	// терминал в каноне: settlement-тег, provenance yandex
	id, ok := ms.ListTerminalIDByCode(ctx, "yandex", "c22165")
	if !ok {
		t.Fatalf("терминал c кодом c22165 не в каноне")
	}
	tags, err := ms.GetTerminalTags(ctx, id)
	if err != nil || tags["settlement"] != "Барнаул" {
		t.Fatalf("settlement-тег: %v %v", tags, err)
	}

	// идемпотентность: повторный прогон с чистым skeleton-пулом находит
	// терминал в каноне по коду — reused, не дубликат.
	sum2, _, err := GapFillEndpoints(ctx, ms, []AttachTerminal{nsk}, endpointTrips(), GapFillConfig{DumpPath: dumpFixture(t)})
	if err != nil {
		t.Fatalf("gapfill rerun: %v", err)
	}
	if sum2.Added != 0 || sum2.Reused != 1 {
		t.Fatalf("повторный прогон: added=%d reused=%d, want 0/1", sum2.Added, sum2.Reused)
	}
	cnt, _ := ms.ListTerminalIDByCode(ctx, "yandex", "c22165")
	if cnt != id {
		t.Fatalf("дубликат: id=%d vs %d", cnt, id)
	}
}

func TestGapFillFullPipeline(t *testing.T) {
	// Полный RunTripsSync: раньше рейс падал в skeleton_gap, теперь
	// gap-fill закрывает конец и рейс промоутится.
	ms := memory.NewMemoryStore()
	seedCanonTerminal(t, ms)
	trips := endpointTrips()
	cfg := runCfg()
	cfg.GapFill = GapFillConfig{DumpPath: dumpFixture(t)}
	ctx := context.Background()
	sum, err := RunTripsSync(ctx, ms, ms, trips, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sum.GapFill == nil || sum.GapFill.Added != 1 {
		t.Fatalf("gap_fill summary: %+v", sum.GapFill)
	}
	if sum.Persist.Trips != 1 {
		t.Fatalf("persist=%+v want trips=1 (рейс обязан промоутиться)", sum.Persist)
	}
}

func TestGapFillNoDumpPathSkipped(t *testing.T) {
	ms := memory.NewMemoryStore()
	nsk := seedCanonTerminal(t, ms)
	sum, terms2, err := GapFillEndpoints(context.Background(), ms, []AttachTerminal{nsk}, endpointTrips(), GapFillConfig{})
	if err != nil {
		t.Fatalf("gapfill: %v", err)
	}
	if sum.DumpStations != 0 || len(terms2) != 1 {
		t.Fatalf("пустой DumpPath: шаг обязан быть прозрачным, сумма=%+v", sum)
	}
}

func TestGapFillOnlyTerminalClasses(t *testing.T) {
	// bus_stop и станции без координат из дампа не создаются (пилот §5.4)
	ms := memory.NewMemoryStore()
	nsk := seedCanonTerminal(t, ms)
	trips := endpointTrips()
	// конец с кодом городской остановки (bus_stop в дампе)
	trips[0].Stops[1].Codes = []model.AdaptedIdentifier{{System: "yandex", CodeType: "yandex_code", Code: "c_bus_stop_1"}}
	sum, _, err := GapFillEndpoints(context.Background(), ms, []AttachTerminal{nsk}, trips, GapFillConfig{DumpPath: dumpFixture(t)})
	if err != nil {
		t.Fatalf("gapfill: %v", err)
	}
	if sum.Added != 0 || sum.SkippedNoDump != 1 {
		t.Fatalf("bus_stop не терминал: added=%d skipped=%d (сумма=%+v)", sum.Added, sum.SkippedNoDump, sum)
	}
}

func TestGapFillReusesExistingCanonical(t *testing.T) {
	// Код уже в каноне (вне skeleton-выборки): дубликат не создаётся,
	// существующий ID попадает в attach-пул.
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	nsk := seedCanonTerminal(t, ms)
	existing, err := ms.UpsertTerminal(ctx, store.TerminalRow{Lat: 53.35, Lon: 83.75, EnrichmentStatus: "identity_only"}, map[string]string{"ru": "Барнаул, автовокзал"}, []model.AdaptedIdentifier{{System: "yandex", CodeType: "yandex_code", Code: "c22165"}})
	if err != nil {
		t.Fatal(err)
	}
	sum, terms2, err := GapFillEndpoints(ctx, ms, []AttachTerminal{nsk}, endpointTrips(), GapFillConfig{DumpPath: dumpFixture(t)})
	if err != nil {
		t.Fatalf("gapfill: %v", err)
	}
	if sum.Added != 0 || sum.Reused != 1 {
		t.Fatalf("added=%d reused=%d want 0/1 (сумма=%+v)", sum.Added, sum.Reused, sum)
	}
	var found bool
	for _, t2 := range terms2 {
		if t2.ID == existing {
			found = true
		}
	}
	if !found {
		t.Fatalf("существующий терминал %d не попал в пул", existing)
	}
}

package skeleton

import (
	"os"
	"path/filepath"
	"testing"

	"travelmcp/internal/model"
)

func rec(name string, lat, lon float64, extra map[string]string, ids ...model.AdaptedIdentifier) model.AdaptedRecord {
	r := model.AdaptedRecord{
		Kind:        model.AdaptedTerminal,
		NameRu:      name,
		Source:      "test",
		Identifiers: ids,
		Extra:       extra,
	}
	if lat != 0 || lon != 0 {
		la, lo := lat, lon
		r.Lat, r.Lon = &la, &lo
	}
	return r
}

func TestCollapseStopArea(t *testing.T) {
	in := []model.AdaptedRecord{
		rec("Кемерово автовокзал", 55.3416, 86.061, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "1"}),
		rec("Кемерово автовокзал", 55.3417, 86.0612, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "2"}),
		rec("Новосибирск автовокзал", 55.0411, 83.0274, map[string]string{"transport_type": "bus", "settlement": "Новосибирск"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "3"}),
	}
	out := CollapseStopArea(in)
	if len(out) != 2 {
		t.Fatalf("ожидалось 2 терминала после схлопывания stop_area, получено %d", len(out))
	}
	if len(out[0].Identifiers) != 2 {
		t.Fatalf("идентификаторы схлопнутых записей должны объединяться: %+v", out[0].Identifiers)
	}
}

func TestCollapseAcrossCellBoundary(t *testing.T) {
	in := []model.AdaptedRecord{
		rec("Собор", 53.8799287, 86.6199482, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "10002208626"}),
		rec("Собор", 53.8794164, 86.6203076, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "10002208627"}),
	}
	out := CollapseStopArea(in)
	if len(out) != 1 {
		t.Fatalf("остановки через границу геоклетки (61м) обязаны схлопываться, получено %d", len(out))
	}
	if len(out[0].Identifiers) != 2 {
		t.Fatalf("идентификаторы обязаны объединяться: %+v", out[0].Identifiers)
	}
}

func TestCollapseFarSameNameKeptSeparate(t *testing.T) {
	in := []model.AdaptedRecord{
		rec("Вокзал", 55.0, 86.0, map[string]string{"transport_type": "bus"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "1"}),
		rec("Вокзал", 55.02, 86.0, map[string]string{"transport_type": "bus"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "2"}),
	}
	out := CollapseStopArea(in)
	if len(out) != 2 {
		t.Fatalf("одноимённые точки в 2км обязаны оставаться разными, получено %d", len(out))
	}
}

func TestCollapseBuildingTrainStationIsRail(t *testing.T) {
	in := []model.AdaptedRecord{
		rec("Городская", 55.36308, 86.15763, map[string]string{"transport_type": "rail"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "1"}),
		rec("Городская", 55.36329, 86.15742, map[string]string{"transport_type": "rail"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "2"}),
	}
	out := CollapseStopArea(in)
	if len(out) != 1 {
		t.Fatalf("здание вокзала + узел обязаны схлопываться, получено %d", len(out))
	}
	if got := out[0].Extra["transport_type"]; got != "rail" {
		t.Fatalf("агрегат обязан быть rail без фантомного bus, получено %q", got)
	}
}

func TestJoinEnrichedAndUnverified(t *testing.T) {
	cfg := DefaultJoinConfig()
	osm := []model.AdaptedRecord{
		rec("Кемерово автовокзал", 55.3416, 86.061, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "1"}),
		rec("Тайга станция", 56.05, 85.62, map[string]string{"transport_type": "rail", "settlement": "Тайга"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "2"}),
	}
	yandex := []model.AdaptedRecord{
		rec("Кемерово автовокзал", 55.3417, 86.0612, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s1001"}),
		rec("Только Яндекс остановка", 55.0, 86.0, map[string]string{"transport_type": "bus", "settlement": "Кемерово"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s9999"}),
	}
	res := Join(osm, yandex, cfg)
	if len(res.Canon) != 2 {
		t.Fatalf("canon=%d, ожидалось 2", len(res.Canon))
	}
	if res.Canon[0].Enrichment != Enriched {
		t.Fatalf("первый терминал должен быть enriched, получен %s", res.Canon[0].Enrichment)
	}
	found := false
	for _, id := range res.Canon[0].Record.Identifiers {
		if id.Code == "s1001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("yandex_code должен переноситься в канон: %+v", res.Canon[0].Record.Identifiers)
	}
	if res.Canon[1].Enrichment != IdentityOnly {
		t.Fatalf("второй терминал должен остаться identity_only, получен %s", res.Canon[1].Enrichment)
	}
	if len(res.Unverified) != 1 || res.Unverified[0].PrimaryCode() != "s9999" {
		t.Fatalf("яндекс-терминал без OSM-пары должен уйти в unverified, получено %+v", res.Unverified)
	}
}

func TestJoinNameOnlyNeverVerified(t *testing.T) {
	cfg := DefaultJoinConfig()
	osm := []model.AdaptedRecord{
		rec("Остановка Центральная", 0, 0, map[string]string{"settlement": "Новосибирск"}),
	}
	yandex := []model.AdaptedRecord{
		rec("Остановка Центральная", 55.03, 82.92, map[string]string{"transport_type": "bus", "settlement": "Бердск"}),
	}
	res := Join(osm, yandex, cfg)
	if res.Canon[0].Enrichment != IdentityOnly {
		t.Fatalf("geom-null в urban по одному name — никогда verified, получен %s", res.Canon[0].Enrichment)
	}
	if len(res.Unverified) != 1 {
		t.Fatal("кандидат должен остаться в unverified")
	}
}

func TestJoinCodeMatch(t *testing.T) {
	cfg := DefaultJoinConfig()
	a := rec("Совсем другое название", 55.0, 86.0, nil,
		model.AdaptedIdentifier{System: "yandex", CodeType: "esr_code", Code: "123"})
	b := rec("Другое", 50.0, 40.0, nil,
		model.AdaptedIdentifier{System: "yandex", CodeType: "esr_code", Code: "123"})
	if s := PairScore(a, b, cfg); s != 1 {
		t.Fatalf("совпадение кода — сильнейший сигнал, score=%v", s)
	}
}

func TestJoinDuplicateAmbiguous(t *testing.T) {
	cfg := DefaultJoinConfig()
	osm := []model.AdaptedRecord{
		rec("Автовокзал Центральный", 55.0, 86.0, map[string]string{"transport_type": "bus", "settlement": "Новосибирск"},
			model.AdaptedIdentifier{System: "osm", CodeType: "osm_id", Code: "1"}),
	}
	yandex := []model.AdaptedRecord{
		rec("Автовокзал Центральный", 55.0002, 86.0002, map[string]string{"transport_type": "bus", "settlement": "Новосибирск"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s1"}),
		rec("Автовокзал Центральный Павлов", 55.0004, 86.0003, map[string]string{"transport_type": "bus", "settlement": "Новосибирск"},
			model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: "s2"}),
	}
	res := Join(osm, yandex, cfg)
	if len(res.DuplicateAmbiguous) != 1 {
		t.Fatalf("ожидался 1 duplicate_ambiguous, получено %d (canon=%d)", len(res.DuplicateAmbiguous), len(res.Canon))
	}
	if len(res.Canon) != 0 {
		t.Fatalf("Canon должен быть пуст при duplicate_ambiguous, получено %d", len(res.Canon))
	}
	if res.DuplicateAmbiguous[0].Record.PrimaryCode() != "1" {
		t.Fatalf("лучший кандидат — OSM record (primary), PrimaryCode=%s", res.DuplicateAmbiguous[0].Record.PrimaryCode())
	}
	hasYandex := false
	for _, id := range res.DuplicateAmbiguous[0].Record.Identifiers {
		if id.System == "yandex" && id.Code == "s1" {
			hasYandex = true
		}
	}
	if !hasYandex {
		t.Fatalf("merged record должен содержать yandex_code s1: %+v", res.DuplicateAmbiguous[0].Record.Identifiers)
	}
	if len(res.Unverified) != 1 || res.Unverified[0].PrimaryCode() != "s2" {
		t.Fatalf("второй кандидат уходит в unverified: %v", res.Unverified)
	}
}

func TestInterpolateMidpoint(t *testing.T) {
	stops := []SeqStop{
		{Name: "A", Seq: 1, Lat: 55.0, Lon: 86.0, Matched: true},
		{Name: "B", Seq: 2, Matched: false},
		{Name: "C", Seq: 3, Lat: 56.0, Lon: 87.0, Matched: true},
	}
	pos, ok := InterpolatePosition(stops, 1)
	if !ok {
		t.Fatal("ожидалась интерполяция между двумя сматченными соседями")
	}
	if pos.Lat != 55.5 || pos.Lon != 86.5 {
		t.Fatalf("seq-пропорционально середина: получено %v %v", pos.Lat, pos.Lon)
	}
	if pos.Origin != "seed" || pos.Confidence >= 0.6 || pos.Method != "seq_proportional" {
		t.Fatalf("оценка обязана быть низкодостоверной seed: %+v", pos)
	}
}

func TestInterpolateDurationShare(t *testing.T) {
	stops := []SeqStop{
		{Name: "A", Seq: 1, Lat: 0.0, Lon: 0.0, Matched: true, Duration: 10},
		{Name: "B", Seq: 2, Matched: false, Duration: 30},
		{Name: "C", Seq: 3, Lat: 10.0, Lon: 0.0, Matched: true},
	}
	pos, ok := InterpolatePosition(stops, 1)
	if !ok {
		t.Fatal("ожидалась интерполяция")
	}
	if pos.Method != "duration_distance_share" {
		t.Fatalf("метод=%s", pos.Method)
	}
	if pos.Lat <= 0 || pos.Lat >= 10 {
		t.Fatalf("долевая позиция вне отрезка: %v", pos.Lat)
	}
}

func TestInterpolateNoNeighbor(t *testing.T) {
	stops := []SeqStop{
		{Name: "B", Seq: 1, Matched: false},
		{Name: "C", Seq: 2, Lat: 56.0, Lon: 87.0, Matched: true},
	}
	if _, ok := InterpolatePosition(stops, 0); ok {
		t.Fatal("без обоих соседей интерполяции нет")
	}
}

func TestCoverageGate(t *testing.T) {
	stops := []SeqStop{
		{Name: "A", Matched: true}, {Name: "B", Matched: true},
		{Name: "C", Matched: false}, {Name: "D", Matched: false},
	}
	byRegion := func(s SeqStop) string { return "kuzbass" }
	cov := CoverageByRegion(stops, byRegion)
	if len(cov) != 1 || cov[0].Ratio() != 0.5 {
		t.Fatalf("coverage=%+v", cov)
	}
	if ok, _ := GatePass(cov, 0.4); !ok {
		t.Fatal("gate 0.4 при ratio 0.5 должен проходиться")
	}
	if ok, blocked := GatePass(cov, 0.8); ok || len(blocked) != 1 {
		t.Fatal("gate 0.8 при ratio 0.5 должен блокировать регион")
	}
}

func TestOSMSourceBuildingTrainStation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stations.json")
	data := `[{"id":1,"kind":"way","name":"Городская","tags":{"building":"train_station","public_transport":"station"},"lat":55.36329,"lon":86.15742},
{"id":2,"kind":"node","name":"Городская","tags":{"public_transport":"station","railway":"station"},"lat":55.36308,"lon":86.15763}]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := OSMSource{Path: path}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("здание + узел одной станции — один терминал, получено %d", len(got))
	}
	if got[0].Extra["transport_type"] != "rail" {
		t.Fatalf("тип обязан быть rail, получено %q", got[0].Extra["transport_type"])
	}
}

func TestOSMSourceLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stations.json")
	data := `[{"id":1,"kind":"node","name":"Кемерово автовокзал","tags":{"highway":"bus_stop"},"lat":55.3416,"lon":86.061},
{"id":2,"kind":"node","name":"","tags":{"highway":"bus_stop"},"lat":55.0,"lon":86.0}]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := OSMSource{Path: path}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].NameRu != "Кемерово автовокзал" {
		t.Fatalf("безымянные отбрасываются: %+v", got)
	}
	if got[0].Kind != model.AdaptedTerminal || got[0].Source != "osm" {
		t.Fatalf("запись обязана быть AdaptedTerminal/osm: %+v", got[0])
	}
}

func TestYandexDumpSourceLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yandex.json")
	data := `{"countries":[{"title":"Россия","regions":[{"title":"Кузбасс","settlements":[{"title":"Кемерово","stations":[{"title":"Кемерово автовокзал","longitude":86.061,"latitude":55.3416,"transport_type":"bus","station_type":"bus_station","codes":{"yandex_code":"s1001","esr_code":""}}]}]}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := YandexDumpSource{Path: path}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("станций=%d", len(got))
	}
	if got[0].PrimaryCode() != "s1001" || got[0].Extra["settlement"] != "Кемерово" || got[0].Extra["region"] != "Кузбасс" {
		t.Fatalf("иерархия/коды не разобраны: %+v", got[0])
	}
}

func TestYandexDumpSourceStringCoords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yandex.json")
	data := `{"countries":[{"title":"Россия","regions":[{"title":"R","settlements":[{"title":"S","stations":[
{"title":"Без координат","longitude":"","latitude":"","transport_type":"bus","station_type":"stop","codes":{}},
{"title":"Строковые координаты","longitude":"86.061","latitude":"55.3416","transport_type":"bus","station_type":"stop","codes":{}}]}]}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := YandexDumpSource{Path: path}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("станций=%d", len(got))
	}
	if got[0].HasCoords() {
		t.Fatalf("пустые координаты — HasCoords=false: %+v", got[0])
	}
	if !got[1].HasCoords() || *got[1].Lat != 55.3416 || *got[1].Lon != 86.061 {
		t.Fatalf("строковые координаты не разобраны: %+v", got[1])
	}
}

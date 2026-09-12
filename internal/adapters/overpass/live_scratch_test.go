package overpass

import (
	"encoding/json"
	"os"
	"testing"
)

// TestScratchLiveRoute101 — офлайн-проверка живой пачки O-6 (не CI: файла нет в CI).
func TestScratchLiveRoute101(t *testing.T) {
	data, err := os.ReadFile("../../../data/overpass/raw/route_101.json")
	if err != nil {
		t.Skip("живая пачка O-6 не собрана (data/overpass не коммитится)")
	}
	recs, err := ParseRouteResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("живая пачка обязана давать записи")
	}
	var trip AdaptedTripData
	if err := json.Unmarshal(recs[0].Raw, &trip); err != nil {
		t.Fatal(err)
	}
	if len(trip.Stops) < 2 {
		t.Fatalf("городской маршрут без остановок: %+v", trip)
	}
	// issue #11: координаты стопов и геометрия обязаны наполняться из
	// node/way элементов пачки (раньше выбрасывались парсером).
	coordStops := 0
	for _, s := range trip.Stops {
		if s.Lat != 0 || s.Lon != 0 {
			coordStops++
		}
	}
	if coordStops == 0 {
		t.Fatal("живая пачка содержит координаты стопов, парсер обязан их сохранять")
	}
	if len(trip.Geometry) == 0 {
		t.Fatal("живая пачка содержит way-члены геометрии, полилиния обязана строиться")
	}
	t.Logf("records=%d, first: ref=%s operator=%q stops=%d (with coords: %d) geometry_points=%d",
		len(recs), trip.Ref, trip.Operator, len(trip.Stops), coordStops, len(trip.Geometry))
}

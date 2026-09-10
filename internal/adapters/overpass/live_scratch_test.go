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
	t.Logf("records=%d, first: ref=%s operator=%q stops=%d", len(recs), trip.Ref, trip.Operator, len(trip.Stops))
}

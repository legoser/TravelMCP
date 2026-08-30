package geo

import (
	"math"
	"testing"

	"travelmcp/internal/model"
)

func TestHaversinePermEkb(t *testing.T) {
	perm := model.Coords{Lat: 58.0, Lon: 56.25}
	ekb := model.Coords{Lat: 56.83, Lon: 60.6}
	got := Haversine(perm, ekb)
	if got < 280 || got > 340 {
		t.Fatalf("Perm–Ekaterinburg ~300 km, got %.1f km", got)
	}
}

func TestHaversineZero(t *testing.T) {
	c := model.Coords{Lat: 58.0135, Lon: 56.2495}
	if got := Haversine(c, c); got != 0 {
		t.Fatalf("zero distance expected, got %.4f", got)
	}
}

func TestNearestStops(t *testing.T) {
	stops := map[string]*model.Stop{
		"far":  {ID: "far", Lat: 58.5, Lon: 56.5},
		"near": {ID: "near", Lat: 58.01, Lon: 56.25},
	}
	got := NearestStops(stops, model.Coords{Lat: 58.0135, Lon: 56.2495}, 30, 1)
	if len(got) != 1 || got[0].ID != "near" {
		t.Fatalf("want near, got %+v", got)
	}
}

func TestNearestStopsTooFar(t *testing.T) {
	stops := map[string]*model.Stop{
		"s": {ID: "s", Lat: 58.5, Lon: 56.5},
	}
	if got := NearestStops(stops, model.Coords{Lat: 58.0135, Lon: 56.2495}, 30, 1); len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestWalkTimeMinutes(t *testing.T) {
	d := Haversine(model.Coords{Lat: 58.0135, Lon: 56.2495}, model.Coords{Lat: 58.0135, Lon: 56.2495})
	if got := WalkTimeMinutes(d); got != 0 {
		t.Fatalf("want 0 min walk, got %d", got)
	}
	if got := WalkTimeMinutes(1.0); got < 11 || got > 15 {
		t.Fatalf("1 km in ~12 min at 5 km/h, got %d", got)
	}
	if got := WalkTimeMinutes(math.MaxFloat64); got != math.MaxInt {
		t.Fatalf("overflow should cap, got %d", got)
	}
}

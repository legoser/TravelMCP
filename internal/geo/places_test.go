package geo

import (
	"math"
	"testing"
	"time"

	"travelmcp/internal/model"
)

func near(t *testing.T, got model.Coords, wantLat, wantLon, eps float64) {
	t.Helper()
	if math.Abs(got.Lat-wantLat) > eps || math.Abs(got.Lon-wantLon) > eps {
		t.Fatalf("resolve = (%.5f, %.5f), want ~ (%.5f, %.5f)", got.Lat, got.Lon, wantLat, wantLon)
	}
}

func TestResolveHubPlaces(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query    string
		lat, lon float64
	}{
		{"Юрга", 55.7132, 84.9023},
		{"Барнаул", 53.3523, 83.7591},
		{"Новосибирск", 55.0411, 83.0274},
		{"Томск", 56.4613, 84.9914},
		{"Кемерово", 55.3416, 86.0610},
		{"Горно-Алтайск", 51.9557, 85.9416},
	}
	for _, c := range cases {
		got, ok := g.Resolve(c.query)
		if !ok {
			t.Fatalf("Resolve(%q) = not found", c.query)
		}
		near(t, got, c.lat, c.lon, 0.02)
	}
}

func TestResolveAddsProviderStops(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Resolve("Екатеринбург"); ok {
		t.Fatal("Екатеринбург должен резолвиться только после добавления остановок провайдера")
	}
	g.Add("Екатеринбург, Автовокзал", 56.8340, 60.5970)
	g.Add("Пермь, Центральная площадь", 58.0135, 56.2495)

	got, ok := g.Resolve("Екатеринбург")
	if !ok {
		t.Fatal("Resolve(Екатеринбург) = not found")
	}
	near(t, got, 56.8340, 60.5970, 0.001)

	got, ok = g.Resolve("Пермь")
	if !ok {
		t.Fatal("Resolve(Пермь) = not found")
	}
	near(t, got, 58.0135, 56.2495, 0.001)
}

func TestResolveUnknown(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"", "   ", "Нигдегород", "Заснежье-3"} {
		if _, ok := g.Resolve(q); ok {
			t.Fatalf("Resolve(%q) должен не находиться", q)
		}
	}
}

func TestResolveDisambiguatesOmskFromTomsk(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	omsk, ok := g.Resolve("Омск")
	if !ok {
		t.Fatal("Resolve(Омск) = not found")
	}
	near(t, omsk, 54.9986, 73.2812, 0.02)

	tomsk, ok := g.Resolve("Томск")
	if !ok {
		t.Fatal("Resolve(Томск) = not found")
	}
	near(t, tomsk, 56.4613, 84.9914, 0.02)

	if omsk.Lat == tomsk.Lat && omsk.Lon == tomsk.Lon {
		t.Fatal("Омск и Томск резолвятся в одинаковые координаты — подстроковое совпадение 'омск' в 'томск' не отфильтровано")
	}
}

func TestDefaultGazetteerLoadsStable(t *testing.T) {
	g1, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	g2, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	if len(g1.places) == 0 {
		t.Fatal("embedded gazetteer is empty")
	}
	if len(g1.places) != len(g2.places) {
		t.Fatalf("places length changed: %d vs %d", len(g1.places), len(g2.places))
	}
}

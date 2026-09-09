package geo

import (
	"context"
	"testing"
)

type fakeSettlementSrc struct {
	list []Settlement
	err  error
}

func (f fakeSettlementSrc) ListSettlements(ctx context.Context) ([]Settlement, error) {
	return f.list, f.err
}

func TestLoadSettlementsFiltersNonPlausible(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	src := fakeSettlementSrc{list: []Settlement{
		{Name: "Новокузнецк", Lat: 53.7465, Lon: 87.1172},
		{Name: "детский", Lat: 43.0, Lon: 43.0},
		{Name: "торговый", Lat: 43.0, Lon: 43.0},
		{Name: "Гусиноозер", Lat: 51.1, Lon: 106.5},
		{Name: "Ом", Lat: 55.0, Lon: 73.0},
		{Name: "вова", Lat: 55.0, Lon: 73.0},
	}}
	added, skipped, err := g.LoadSettlements(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || skipped != 4 {
		t.Fatalf("added=%d skipped=%d, want 1/4 (Новокузнецк новый, остальное мусор)", added, skipped)
	}
	c, ok := g.Resolve("Новокузнецк")
	if !ok || c.Lat != 53.7465 {
		t.Fatalf("Новокузнецк из БД не резолвится: %v %v", c, ok)
	}
}

func TestLoadSettlementsOverridesStaticEntry(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	// статическая запись Бийска (52.5488/85.2177 — старая точка реестра)
	// должна замениться координатой канона (терминал с живыми рейсами)
	src := fakeSettlementSrc{list: []Settlement{
		{Name: "Бийск", Lat: 52.5340, Lon: 85.1793},
	}}
	added, _, err := g.LoadSettlements(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("Бийск должен был заменить статическую запись, а не добавиться: added=%d", added)
	}
	c, ok := g.Resolve("Бийск")
	if !ok {
		t.Fatal("Бийск не найден")
	}
	if c.Lat != 52.5340 || c.Lon != 85.1793 {
		t.Fatalf("координаты Бийска не заменены: %v", c)
	}
}

func TestWordBoundContains(t *testing.T) {
	cases := []struct {
		name, query string
		want        bool
	}{
		{"томск", "омск", false},  // подстрока внутри слова — не матч
		{"г. омск", "омск", true}, // граница: точка + начало слова
		{"омск центральный", "омск", true},
		{"новосибирск", "сибирск", false},      // внутри слова
		{"гусиноозёрск", "гусиноозерск", true}, // нормализованы одинаково
		{"", "омск", false},
		{"омск", "", false},
	}
	for _, c := range cases {
		if got := wordBoundContains(c.name, c.query); got != c.want {
			t.Errorf("wordBoundContains(%q, %q) = %v, want %v", c.name, c.query, got, c.want)
		}
	}
}

func TestResolveDetailedMethods(t *testing.T) {
	g, err := DefaultGazetteer()
	if err != nil {
		t.Fatal(err)
	}
	if r := g.ResolveDetailed("Томск"); !r.Found || r.Method == "none" || r.Method == "substring" {
		t.Fatalf("Томск: %+v", r)
	}
	if r := g.ResolveDetailed("НетТакогоГорода999"); r.Found {
		t.Fatalf("несуществующий: %+v", r)
	}
}

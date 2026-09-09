package geo

import (
	"context"
	"strings"
	"unicode"

	"travelmcp/internal/model"
)

// SettlementSource — источник населённых пунктов для рантайм-газетира.
// Узкий интерфейс под потребителя (SOLID): Gazetteer'у не нужен широкий Store,
// только список поселений с координатами. Реализация в адаптере Store.
type SettlementSource interface {
	ListSettlements(ctx context.Context) ([]Settlement, error)
}

// Settlement — населённый пункт с координатой-представителем (автовокзал/станция).
type Settlement struct {
	Name string
	Lat  float64
	Lon  float64
}

// LoadSettlements добавляет в Gazetteer поселения из источника (канон Postgres).
// Каждое имя нормализуется и проходит фильтр мусора (см. isPlausibleSettlement):
// теги settlement приходят из адресных строк и содержат прилагательные
// («детский», «торговый») и обрывки («Гусиноозер»), которые не должны
// резолвить пользовательские запросы. Совпадающее по имени статическое
// место ЗАМЕНЯЕТСЯ записью из канона (БД — источник истины, актуальные
// координаты терминала с живыми рейсами), а не дублируется.
func (g *Gazetteer) LoadSettlements(ctx context.Context, src SettlementSource) (added int, skipped int, err error) {
	list, err := src.ListSettlements(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, s := range list {
		if !isPlausibleSettlement(s.Name) {
			skipped++
			continue
		}
		if g.upsert(s.Name, s.Lat, s.Lon) {
			added++
		}
	}
	return added, skipped, nil
}

// upsert вставляет место или обновляет координаты существующего с тем же
// нормализованным именем. Возвращает true, если появилась новая запись.
func (g *Gazetteer) upsert(name string, lat, lon float64) bool {
	key := normQuery(strings.Split(name, ",")[0])
	for i := range g.places {
		if normQuery(g.places[i].Name) == key || containsAlias(g.places[i], key) {
			g.places[i].Lat, g.places[i].Lon = lat, lon
			return false
		}
	}
	g.Add(name, lat, lon)
	return true
}

func containsAlias(p Place, key string) bool {
	for _, a := range p.Aliases {
		if normQuery(a) == key {
			return true
		}
	}
	return false
}

// isPlausibleSettlement отсекает не-топонимы из адресных тегов: прилагательные
// и обрывки в нижнем регистре («детский», «вова», «дачи»), номера/цифры,
// слишком короткие. Топонимы в БД всегда с заглавной буквы; реальных
// поселений из 3 рун нет (данные зоны — СФО), 4 — граничат («Шира», «Туим»).
func isPlausibleSettlement(name string) bool {
	n := strings.TrimSpace(name)
	r := []rune(n)
	if len(r) < 4 || len(r) > 60 {
		return false
	}
	if !unicode.IsUpper(r[0]) {
		return false
	}
	return true
}

// wordBoundContains — substring-фолбэк с границей слова: запрос «Омск»
// матчит «г. Омск»/«Омск центр», но не «Томск» (нет границы перед «о»).
// Длина запроса при этом не режет составные имена («Ново-» префиксы).
func wordBoundContains(name, query string) bool {
	nr := []rune(normQuery(name))
	qr := []rune(normQuery(query))
	if len(qr) == 0 || len(qr) > len(nr) {
		return false
	}
	for i := 0; i+len(qr) <= len(nr); i++ {
		if string(nr[i:i+len(qr)]) != string(qr) {
			continue
		}
		before := i == 0 || !isWordRune(nr[i-1])
		after := i+len(qr) >= len(nr) || !isWordRune(nr[i+len(qr)])
		if before && after {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я', r == 'ё', r == 'Ё':
		return true
	}
	return false
}

// ResolveWithContext — Resolve с диагностикой источника решения для логов.
type ResolveResult struct {
	Coords  model.Coords
	Found   bool
	Method  string // exact | substring | none
	Matched string
}

func (g *Gazetteer) ResolveDetailed(query string) ResolveResult {
	q := normQuery(query)
	if q == "" {
		return ResolveResult{Method: "none"}
	}
	var short *Place
	shortLen := 0
	var shortName string
	for i := range g.places {
		p := &g.places[i]
		for _, a := range append(p.Aliases, p.Name) {
			n := normQuery(a)
			if n == q {
				return ResolveResult{Coords: model.Coords{Lat: p.Lat, Lon: p.Lon}, Found: true, Method: "exact", Matched: p.Name}
			}
			if exactMatch(n, q) {
				return ResolveResult{Coords: model.Coords{Lat: p.Lat, Lon: p.Lon}, Found: true, Method: "token", Matched: p.Name}
			}
			if wordBoundContains(n, q) {
				if short == nil || len(n) < shortLen {
					short = p
					shortLen = len(n)
					shortName = p.Name
				}
			}
		}
	}
	if short != nil {
		return ResolveResult{Coords: model.Coords{Lat: short.Lat, Lon: short.Lon}, Found: true, Method: "substring", Matched: shortName}
	}
	return ResolveResult{Method: "none"}
}

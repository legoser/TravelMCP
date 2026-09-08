package sync

import (
	"log/slog"
	"strings"

	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

// ResolveStopCoords — стадия до attach: стопы среза без координат получают
// геометрию сматченного терминала скелета (имя + поселение через
// SameSettlement, транспортная совместимость). Скелет уже несёт
// верифицированные координаты OSM×Yandex — это внутренний источник,
// внешних вызовов нет, поэтому без квот. Стоп остаётся без координат,
// если уверенного матча нет: угаданных точек не фабрикуем (§5.4).
//
// Матчинг консервативный: требует и имя (Sim ≥ 0.6), и совпадение
// поселения; координаты берутся у лучшего кандидата при margin над
// вторым ≥ 0.15 (одинокий кандидат проходит без margin).
func ResolveStopCoords(trips []model.FlatTrip, terms []AttachTerminal, source string) (filled int, stops int) {
	if len(terms) == 0 {
		return 0, 0
	}
	bySettlement := map[string][]int{}
	var noSettle []int
	for i := range terms {
		s := namesim.Core(terms[i].Settlement)
		if s != "" {
			bySettlement[s] = append(bySettlement[s], i)
			continue
		}
		noSettle = append(noSettle, i)
	}
	for ti := range trips {
		for si := range trips[ti].Stops {
			s := &trips[ti].Stops[si]
			if s.Lat != nil && s.Lon != nil {
				continue
			}
			stops++
			la, lo, ok := resolveOne(*s, terms, bySettlement, noSettle, source)
			if !ok {
				continue
			}
			s.Lat, s.Lon = &la, &lo
			filled++
		}
	}
	return filled, stops
}

func resolveOne(s model.FlatStop, terms []AttachTerminal, bySettlement map[string][]int, noSettle []int, source string) (float64, float64, bool) {
	settle := namesim.ExtractSettlement(s.Name)
	var pool []int
	if settle != "" {
		for core, idxs := range bySettlement {
			if namesim.SameSettlement(settle, core) {
				pool = append(pool, idxs...)
			}
		}
	}
	if len(pool) == 0 {
		pool = noSettle
	}
	if len(pool) == 0 {
		return 0, 0, false
	}
	stopType := model.InferStopType(s.Name)
	best := -1
	bestScore := 0.0
	second := 0.0
	for _, ti := range pool {
		t := terms[ti]
		score := namesim.Similarity(s.Name, t.Name)
		if score < 0.6 {
			continue
		}
		if settle != "" && t.Settlement != "" && !namesim.SameSettlement(settle, t.Settlement) {
			continue
		}
		// Транспортный сигнал: у «Кемеровский АВ» имя-скоринг не отличает
		// автовокзал от ж/д вокзала и аэропорта (Core вырезает типы),
		// бонус за совместимость типа разводит тёзок одного города.
		// Тип стопа — из имени (реестр без явного типа); тип терминала —
		// transport_types канона + объектное слово в имени терминала:
		// bus-стопов у ж/д вокзала и аэропорта много, различает только
		// явное «автовокзал/автостанция» в имени против hub-стопа.
		if stopTypeSignalMatch(stopType, t.Name, t.Transport) {
			score += 0.25
		}
		// Контраст-фича: различающее слово терминала, которого нет в стопе
		// («Летний», «ТЦ», «Ж/Д», «Аэропорт»), штрафует — «Кемеровский АВ»
		// реестра не тождественен «Летнему автовокзалу» и «автовокзалу
		// Ж/Д вокзала», хотя Core их не различает.
		if hasContrastWord(s.Name, t.Name) {
			score -= 0.25
		}
		if score > bestScore {
			second = bestScore
			bestScore = score
			best = ti
		} else if score > second {
			second = score
		}
	}
	if best < 0 || bestScore < 0.6 {
		return 0, 0, false
	}
	if len(pool) > 1 && bestScore-second < 0.15 && second >= 0.6 {
		return 0, 0, false
	}
	t := terms[best]
	if t.Lat == nil || t.Lon == nil {
		return 0, 0, false
	}
	slog.Debug("trips georesolve: стоп получил координаты терминала",
		"stop", s.Name, "terminal", t.Name, "score", bestScore, "stop_type", string(stopType))
	_ = source
	return *t.Lat, *t.Lon, true
}

// stopTypeSignalMatch — сигнальная фича для hub/airport-стопов реестра:
// hub-стоп (АВ/автостанция в имени) обязан матчиться на терминал со словом
// «автовокзал»/«автостанция» в имени (не «ж/д вокзал»/«аэропорт», где bus —
// это остановка при объекте другого типа); airport-стоп — на «аэропорт».
// station-стопы (вокзал без уточнения) — без сигнала, имя само различает.
func stopTypeSignalMatch(stopType model.StopType, terminalName, terminalTransport string) bool {
	n := strings.ToLower(terminalName)
	switch stopType {
	case model.StopTypeHub:
		return strings.Contains(n, "автовокзал") || strings.Contains(n, "автостанция")
	case model.StopTypeAirport:
		return strings.Contains(n, "аэропорт")
	}
	return false
}

// contrastWords — слова, различающие тёзок одного города: если они есть в
// имени терминала, но не в имени стопа, это другой объект (не синонимичная
// форма того же). Реестр «Кемеровский АВ» — главный автовокзал; «Летний
// автовокзал» — отдельный терминал в другом месте города.
var contrastWords = []string{
	"летний", "летний автовокзал", "ж/д", "железнодорожн", "аэропорт",
	"тц ", "тц\"", "тц«", "юго-западн", "речной", "восточн", "западн", "северн",
	"привокзальн", "летний",
}

// hasContrastWord — терминал содержит различающее слово, отсутствующее
// в имени стопа: кандидат — тёзка другого объекта.
func hasContrastWord(stopName, terminalName string) bool {
	s := strings.ToLower(stopName)
	n := strings.ToLower(terminalName)
	for _, w := range contrastWords {
		if strings.Contains(n, w) && !strings.Contains(s, w) {
			return true
		}
	}
	return false
}

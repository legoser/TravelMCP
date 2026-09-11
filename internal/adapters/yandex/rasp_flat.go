package yandex

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"travelmcp/internal/model"
)

// RaspFlattenConfig — параметры конвертации ниток в flat-рейсы.
type RaspFlattenConfig struct {
	// Region — значение FlatStop.Region для всех стопов (регион сбора).
	Region string
}

// RaspFlattenResult — нитка + итог конвертации.
type RaspFlattenResult struct {
	Trip   *model.FlatTrip
	State  string // promoted | restricted_days | no_times | empty
	Detail string
}

// weekdayRU — канон дней недели flat-формата: вс=0..сб=6 (model.FormatWeekdays).
var weekdayRU = map[string]int{
	"вс": 0, "пн": 1, "вт": 2, "ср": 3, "чт": 4, "пт": 5, "сб": 6,
}

// ParseRaspDays — строка days Яндекса → дни недели (вс=0..сб=6).
// Поддерживает: «ежедневно», «ежедневно, кроме сб, вс», «пн, ср, пт»,
// «ср» (одиночный). Календарные даты («4, 6, 8 … сентября») и чётность
// («по чётным») в дни недели не ложатся → ok=false (нитка restricted).
func ParseRaspDays(days string) ([]int, bool) {
	s := strings.TrimSpace(strings.ToLower(days))
	s = strings.ReplaceAll(s, "\u00a0", " ")
	if s == "" || s == "ежедневно" {
		return allWeek(), true
	}
	if strings.Contains(s, "числ") || strings.Contains(s, "чёт") || strings.Contains(s, "чет") {
		return nil, false
	}
	if base, except, found := strings.Cut(s, "кроме"); found {
		basePart := strings.TrimSpace(base)
		if basePart == "" || strings.Contains(basePart, "ежедневно") {
			basePart = "ежедневно"
		}
		week, ok := parseDayListRU(basePart)
		if !ok {
			return nil, false
		}
		ex, ok := parseDayListRU(except)
		if !ok {
			return nil, false
		}
		black := map[int]bool{}
		for _, d := range ex {
			black[d] = true
		}
		out := week[:0]
		for _, d := range week {
			if !black[d] {
				out = append(out, d)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	}
	// список дат («4, 6, 8 сентября») содержит не-дни-недели токены
	if week, ok := parseDayListRU(s); ok {
		return week, true
	}
	return nil, false
}

func parseDayListRU(s string) ([]int, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "ежедневно" {
		return allWeek(), true
	}
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(s, ",") {
		for _, tok := range strings.Fields(part) {
			n, ok := weekdayRU[tok]
			if !ok {
				return nil, false
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	sort.Ints(out)
	return out, true
}

func allWeek() []int { return []int{0, 1, 2, 3, 4, 5, 6} }

// parseRaspClock — «2006-01-02 15:04:05» | «15:04:05» | «2006-01-02T15:04:05+07:00» → минуты суток.
func parseRaspClock(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, 'T'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, "+Z"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if h < 0 || h > 47 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// RaspTripNK — стабильный ключ нитки: uid Яндекса.
func RaspTripNK(uid string) string { return uid }

// SyntheticRouteKeyForRasp — route NK без номера нитки: title + перевозчик.
func SyntheticRouteKeyForRasp(title, carrierTitle string) string {
	key := title
	if carrierTitle != "" {
		key += "|" + carrierTitle
	}
	return strings.ToLower(strings.Join(strings.Fields(key), " "))
}

// FlattenRaspThread — нитка → FlatTrip. Времена пересчитываются в минуты
// от отправления первого стопа (переход через полночь: отрицательное
// значение +1440). Стоп-коды идут как {system: yandex, code_type:
// yandex_code} — точный code-match против Яндекс-скелета.
func FlattenRaspThread(t *RaspThread, cfg RaspFlattenConfig) RaspFlattenResult {
	if t == nil || len(t.Stops) < 2 {
		return RaspFlattenResult{State: "empty", Detail: "меньше двух стопов"}
	}
	week, ok := ParseRaspDays(t.Days)
	if !ok {
		return RaspFlattenResult{
			State:  "restricted_days",
			Detail: fmt.Sprintf("days=%q: календарные даты/чётность не маппятся в дни недели", t.Days),
		}
	}
	stops := make([]model.FlatStop, 0, len(t.Stops))
	firstDep := -1
	timed := 0
	for _, st := range t.Stops {
		arr, hasArr := clockOf(st.Arrival)
		dep, hasDep := clockOf(st.Departure)
		if firstDep < 0 && hasDep {
			firstDep = dep
		}
		if hasArr || hasDep {
			timed++
		}
		stop := model.FlatStop{
			StopID: st.Station.Code,
			Name:   st.Station.Title,
			Region: cfg.Region,
			Codes: []model.AdaptedIdentifier{
				{System: "yandex", CodeType: "yandex_code", Code: st.Station.Code},
			},
		}
		if hasArr {
			a := arr
			stop.ArrMin = &a
		}
		if hasDep {
			d := dep
			stop.DepMin = &d
		}
		stops = append(stops, stop)
	}
	if timed < 2 || firstDep < 0 {
		return RaspFlattenResult{State: "no_times", Detail: "нет двух timed-стопов"}
	}
	for i := range stops {
		stops[i].ArrMin = shiftFromBase(stops[i].ArrMin, firstDep)
		stops[i].DepMin = shiftFromBase(stops[i].DepMin, firstDep)
	}
	first := stops[0]
	if first.DepMin == nil {
		zero := 0
		first.DepMin = &zero
	}
	carrier := t.Carrier.Title
	trip := &model.FlatTrip{
		RouteNK:   SyntheticRouteKeyForRasp(t.Title, carrier),
		RouteReg:  t.Title,
		Direction: "forward",
		ServiceID: int64(raspCarrierCode(t.Carrier.Code)),
		Run:       1,
		Period:    t.Days,
		Carrier:   carrier,
		RouteFrom: first.Name,
		RouteTo:   stops[len(stops)-1].Name,
		Stops:     stops,
		Weekdays:  week,
	}
	return RaspFlattenResult{Trip: trip, State: "promoted"}
}

func clockOf(s *string) (int, bool) {
	if s == nil {
		return 0, false
	}
	return parseRaspClock(*s)
}

// shiftFromBase — абсолютные минуты суток → относительные от base;
// откат за полночь получает +1440 (вечерний рейс, прибывающий завтра).
func shiftFromBase(v *int, base int) *int {
	if v == nil {
		return nil
	}
	out := *v - base
	if out < 0 {
		out += 24 * 60
	}
	return &out
}

func raspCarrierCode(code int) int {
	if code < 0 {
		return 0
	}
	return code
}

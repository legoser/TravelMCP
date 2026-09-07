package sync

import (
	"math"

	"travelmcp/internal/model"
)

// matchIndex — per-run blocking-индекс терминалов для attach (D-1): пул
// кандидатов стопа = гео-окно (ячейки 0.5°, соседи ±1, планарный pre-filter
// 2км) ∪ код-совпадения ∪ терминалы без координат. Лекало JoinPager, но не
// geo.SpatialIndex.Nearest: голый радиус выкинул бы код-совпадения, а
// golden-кейс «тёзка за 79 км верифицируется по коду» обязан проходить.
type matchIndex struct {
	terms    []AttachTerminal
	cells    map[[2]int][]int // гео-ячейка → индексы терминалов
	noCoords []int            // терминалы без координат (участвуют во всех пулах)
	byCode   map[string][]int // "system|code" → индексы (код-совпадения проходят гео-окно)
}

const matchCellSizeDeg = 0.5
const matchGeoWindowM = 2000

func buildMatchIndex(terms []AttachTerminal) *matchIndex {
	idx := &matchIndex{
		terms:  terms,
		cells:  map[[2]int][]int{},
		byCode: map[string][]int{},
	}
	for i, t := range terms {
		if t.Lat != nil && t.Lon != nil {
			c := [2]int{int(math.Floor(*t.Lat / matchCellSizeDeg)), int(math.Floor(*t.Lon / matchCellSizeDeg))}
			idx.cells[c] = append(idx.cells[c], i)
		} else {
			idx.noCoords = append(idx.noCoords, i)
		}
		for _, id := range t.Codes {
			if id.Code == "" {
				continue
			}
			key := id.System + "|" + id.Code
			idx.byCode[key] = append(idx.byCode[key], i)
		}
	}
	return idx
}

// candidates — пул кандидатов для стопа: гео-окно ∪ код-совпадения ∪
// бескординатные терминалы. Детерминизм: порядок индексов стабильной
// сортировкой (эквивалентно итерации по terms).
func (idx *matchIndex) candidates(stop model.FlatStop, source string) []int {
	seen := map[int]bool{}
	var out []int
	add := func(idxs ...[]int) {
		for _, list := range idxs {
			for _, i := range list {
				if !seen[i] {
					seen[i] = true
					out = append(out, i)
				}
			}
		}
	}
	if stop.Lat != nil && stop.Lon != nil {
		base := [2]int{int(math.Floor(*stop.Lat / matchCellSizeDeg)), int(math.Floor(*stop.Lon / matchCellSizeDeg))}
		for di := -1; di <= 1; di++ {
			for dj := -1; dj <= 1; dj++ {
				add(idx.cells[[2]int{base[0] + di, base[1] + dj}])
			}
		}
	}
	for _, id := range StopCodes(source, stop.OpCode) {
		add(idx.byCode[id.System+"|"+id.Code])
	}
	add(idx.noCoords)
	return out
}

// inGeoWindow — планарная оценка 2км (дёшево, до haversine в ScorePair).
func (idx *matchIndex) inGeoWindow(stopLat, stopLon float64, t AttachTerminal) bool {
	if t.Lat == nil || t.Lon == nil {
		return true
	}
	dLat := (*t.Lat - stopLat) * 111000
	dLon := (*t.Lon - stopLon) * 111000 * cosDeg(stopLat)
	return dLat*dLat+dLon*dLon <= matchGeoWindowM*matchGeoWindowM
}

func cosDeg(latDeg float64) float64 {
	return 1 - latDeg*latDeg*0.0000152
}

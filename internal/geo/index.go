package geo

import (
	"math"
	"runtime"
	"sort"
	"sync"

	"travelmcp/internal/model"
)

const defaultCellSize = 0.01

// Локальные значения индекса (не конфиг): fallbackMaxWalkMinutes — пеший
// лимит, когда запрос не задал (совпадает с planner.max_walk_minutes);
// bruteForceThreshold — ниже этого числа стопов полный перебор дешевле
// индексного (меньше накладных на ячейки).
const (
	fallbackMaxWalkMinutes = 30
	bruteForceThreshold    = 500
)

type SpatialIndex struct {
	cellSize float64
	cells    map[int64][]*model.Stop
}

func NewSpatialIndex(stops map[string]*model.Stop) *SpatialIndex {
	idx := &SpatialIndex{cellSize: defaultCellSize, cells: make(map[int64][]*model.Stop)}
	for _, s := range stops {
		if s.Lat == 0 && s.Lon == 0 {
			continue
		}
		key := cellKey(s.Lat, s.Lon, idx.cellSize)
		idx.cells[key] = append(idx.cells[key], s)
	}
	return idx
}

func cellKey(lat, lon, cellSize float64) int64 {
	x := int64(math.Floor(lon / cellSize))
	y := int64(math.Floor(lat / cellSize))
	return (x << 32) ^ (y & 0xffffffff)
}

// cellKm — размер ячейки в км (cellSize в градусах × MetersPerDegree).
func cellKm(cellSize float64) float64 {
	return cellSize * MetersPerDegree / 1000
}

func (idx *SpatialIndex) Nearest(p model.Coords, maxWalkMinutes, limit int) []*model.Stop {
	maxKm := walkSpeedKmH * float64(maxWalkMinutes) / 60.0
	if maxKm <= 0 {
		maxKm = walkSpeedKmH * fallbackMaxWalkMinutes / 60
	}
	cellRadius := int(math.Ceil(maxKm/cellKm(idx.cellSize))) + 1
	cx := int(math.Floor(p.Lon / idx.cellSize))
	cy := int(math.Floor(p.Lat / idx.cellSize))
	candidates := make([]*model.Stop, 0, 64)
	seen := make(map[string]bool)
	for dx := -cellRadius; dx <= cellRadius; dx++ {
		for dy := -cellRadius; dy <= cellRadius; dy++ {
			key := (int64(cx+dx) << 32) ^ int64(int32(cy+dy))
			for _, s := range idx.cells[key] {
				if seen[s.ID] {
					continue
				}
				seen[s.ID] = true
				if Haversine(p, s.Coordinates()) <= maxKm {
					candidates = append(candidates, s)
				}
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		d1 := Haversine(p, candidates[i].Coordinates())
		d2 := Haversine(p, candidates[j].Coordinates())
		if d1 != d2 {
			return d1 < d2
		}
		return candidates[i].ID < candidates[j].ID
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func NearbyPairs(stops map[string]*model.Stop, maxKm float64) [][2]*model.Stop {
	idx := NewSpatialIndex(stops)
	if len(stops) < bruteForceThreshold {
		var pairs [][2]*model.Stop
		seen := make(map[string]bool)
		for _, a := range stops {
			if a.Lat == 0 && a.Lon == 0 {
				continue
			}
			cx := int(math.Floor(a.Lon / idx.cellSize))
			cy := int(math.Floor(a.Lat / idx.cellSize))
			r := int(math.Ceil(maxKm/cellKm(idx.cellSize))) + 1
			for dx := -r; dx <= r; dx++ {
				for dy := -r; dy <= r; dy++ {
					key := (int64(cx+dx) << 32) ^ int64(int32(cy+dy))
					for _, b := range idx.cells[key] {
						if a.ID >= b.ID {
							continue
						}
						pairKey := a.ID + "|" + b.ID
						if seen[pairKey] {
							continue
						}
						seen[pairKey] = true
						if Haversine(a.Coordinates(), b.Coordinates()) <= maxKm {
							pairs = append(pairs, [2]*model.Stop{a, b})
						}
					}
				}
			}
		}
		return pairs
	}
	keys := make([]string, 0, len(stops))
	for k := range stops {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type chunkResult [][2]*model.Stop
	chunks := make([]chunkResult, len(keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU()*2)
	for i, id := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func(idxPos int, sid string) {
			defer wg.Done()
			defer func() { <-sem }()
			a := stops[sid]
			if a.Lat == 0 && a.Lon == 0 {
				return
			}
			cx := int(math.Floor(a.Lon / idx.cellSize))
			cy := int(math.Floor(a.Lat / idx.cellSize))
			r := int(math.Ceil(maxKm/cellKm(idx.cellSize))) + 1
			var local [][2]*model.Stop
			seenLocal := make(map[string]bool)
			for dx := -r; dx <= r; dx++ {
				for dy := -r; dy <= r; dy++ {
					key := (int64(cx+dx) << 32) ^ int64(int32(cy+dy))
					for _, b := range idx.cells[key] {
						if a.ID >= b.ID {
							continue
						}
						pairKey := a.ID + "|" + b.ID
						if seenLocal[pairKey] {
							continue
						}
						seenLocal[pairKey] = true
						if Haversine(a.Coordinates(), b.Coordinates()) <= maxKm {
							local = append(local, [2]*model.Stop{a, b})
						}
					}
				}
			}
			chunks[idxPos] = local
		}(i, id)
	}
	wg.Wait()
	seen := make(map[string]bool)
	var pairs [][2]*model.Stop
	for _, c := range chunks {
		for _, pr := range c {
			k := pr[0].ID + "|" + pr[1].ID
			if seen[k] {
				continue
			}
			seen[k] = true
			pairs = append(pairs, pr)
		}
	}
	return pairs
}

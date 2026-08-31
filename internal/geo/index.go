package geo

import (
	"math"
	"sort"

	"travelmcp/internal/model"
)

const defaultCellSize = 0.01

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

func (idx *SpatialIndex) Nearest(p model.Coords, maxWalkMinutes, limit int) []*model.Stop {
	maxKm := walkSpeedKmH * float64(maxWalkMinutes) / 60.0
	if maxKm <= 0 {
		maxKm = walkSpeedKmH * 30 / 60
	}
	cellRadius := int(math.Ceil(maxKm/1.11)) + 1
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
	var pairs [][2]*model.Stop
	seen := make(map[string]bool)
	for _, a := range stops {
		if a.Lat == 0 && a.Lon == 0 {
			continue
		}
		cx := int(math.Floor(a.Lon / idx.cellSize))
		cy := int(math.Floor(a.Lat / idx.cellSize))
		r := int(math.Ceil(maxKm/1.11)) + 1
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

package geo

import (
	"math"
	"sort"

	"travelmcp/internal/model"
)

const (
	earthRadiusKm = 6371.0
	walkSpeedKmH  = 5.0
)

func Haversine(a, b model.Coords) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)

	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

func WalkTimeMinutes(distKm float64) int {
	if distKm <= 0 {
		return 0
	}
	minutes := distKm / walkSpeedKmH * 60
	if minutes > float64(math.MaxInt) || math.IsInf(minutes, 0) || math.IsNaN(minutes) {
		return math.MaxInt
	}
	return int(math.Ceil(minutes))
}

func NearestStops(stops map[string]*model.Stop, p model.Coords, maxWalkMinutes, limit int) []*model.Stop {
	maxKm := walkSpeedKmH * float64(maxWalkMinutes) / 60.0
	res := make([]*model.Stop, 0, len(stops))
	for _, s := range stops {
		if Haversine(p, s.Coordinates()) <= maxKm {
			res = append(res, s)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		d1 := Haversine(p, res[i].Coordinates())
		d2 := Haversine(p, res[j].Coordinates())
		if d1 != d2 {
			return d1 < d2
		}
		return res[i].ID < res[j].ID
	})
	if limit > 0 && len(res) > limit {
		res = res[:limit]
	}
	return res
}

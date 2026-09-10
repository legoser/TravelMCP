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

// Геодезические константы для pre-filter окон и blocking-индексов.
// Точные дистанции — Haversine/HaversineM; планарная оценка через
// MetersPerDegree/CosLatApprox — только для дешёвых окон в пару км.
const (
	// MetersPerDegree — метров в градусе широты (сферическое приближение).
	MetersPerDegree = 111000.0
	// JoinCellSizeDeg — сторона гео-ячейки blocking-индекса (skeleton
	// JoinPager, sync matchIndex): 0.5° ≈ 55 км — покрывает окно
	// JoinGeoWindowM с запасом, делит РФ на ~13k ячеек.
	JoinCellSizeDeg = 0.5
	// JoinGeoWindowM — планарное pre-filter окно кандидатов: 4×GeoThreshold
	// (2 км при 500 м) — весь диапазон ненулевой geom-фичи ScorePair.
	JoinGeoWindowM = 2000
)

// cosLatFactor — коэффициент параболической аппроксимации cos(широта):
// cos(lat) ≈ 1 − lat²·cosLatFactor, lat в градусах. Ошибка <1% до 60°.
const cosLatFactor = 0.0000152

// CosLatApprox — поправка долготы на широту для планарных окон.
func CosLatApprox(latDeg float64) float64 {
	return 1 - latDeg*latDeg*cosLatFactor
}

func Haversine(a, b model.Coords) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)

	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

// HaversineM — точное расстояние в метрах по гаверсинусу.
func HaversineM(lat1, lon1, lat2, lon2 float64) float64 {
	return Haversine(model.Coords{Lat: lat1, Lon: lon1}, model.Coords{Lat: lat2, Lon: lon2}) * 1000
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

// WalkRadiusKm — максимальный пешеходный радиус (км) для заданного лимита минут.
func WalkRadiusKm(maxWalkMinutes int) float64 {
	return walkSpeedKmH * float64(maxWalkMinutes) / 60.0
}

func NearestStops(stops map[string]*model.Stop, p model.Coords, maxWalkMinutes, limit int) []*model.Stop {
	maxKm := WalkRadiusKm(maxWalkMinutes)
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

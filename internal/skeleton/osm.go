package skeleton

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

type osmStation struct {
	ID   int64             `json:"id"`
	Kind string            `json:"kind"`
	Name string            `json:"name"`
	Tags map[string]string `json:"tags"`
	Lat  float64           `json:"lat"`
	Lon  float64           `json:"lon"`
}

type OSMSource struct {
	Path string
}

func (s OSMSource) Name() string { return "osm" }

func (s OSMSource) Load() ([]model.AdaptedRecord, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var stations []osmStation
	if err := json.Unmarshal(data, &stations); err != nil {
		return nil, fmt.Errorf("skeleton osm %s: %w", s.Path, err)
	}
	records := make([]model.AdaptedRecord, 0, len(stations))
	for _, st := range stations {
		if strings.TrimSpace(st.Name) == "" {
			continue
		}
		var lat, lon *float64
		if st.Lat != 0 || st.Lon != 0 {
			la, lo := st.Lat, st.Lon
			lat, lon = &la, &lo
		}
		records = append(records, model.AdaptedRecord{
			Kind:   model.AdaptedTerminal,
			NameRu: st.Name,
			Lat:    lat,
			Lon:    lon,
			Source: "osm",
			Identifiers: []model.AdaptedIdentifier{
				{System: "osm", CodeType: "osm_id", Code: fmt.Sprintf("%d", st.ID)},
			},
			Extra: map[string]string{
				"settlement":     namesim.ExtractSettlement(st.Name),
				"transport_type": osmTransportType(st.Tags),
				"object_type":    osmObjectType(st.Tags),
				"osm_kind":       st.Kind,
			},
		})
	}
	return CollapseStopArea(records), nil
}

func osmTransportType(tags map[string]string) string {
	if v, ok := tags["railway"]; ok && (v == "station" || v == "halt") {
		if tags["station"] == "subway" || tags["subway"] == "yes" {
			return "subway"
		}
		return "rail"
	}
	if v, ok := tags["building"]; ok && v == "train_station" {
		if tags["station"] == "subway" || tags["subway"] == "yes" {
			return "subway"
		}
		return "rail"
	}
	if tags["subway"] == "yes" {
		return "subway"
	}
	return "bus"
}

func osmObjectType(tags map[string]string) string {
	if v, ok := tags["public_transport"]; ok && v == "station" {
		return "station"
	}
	if v, ok := tags["amenity"]; ok && v == "bus_station" {
		return "station"
	}
	return "stop"
}

// collapseMaxM — радиус схлопывания одноимённых OSM-точек в stop_area:
// платформы/остановки одного вокзала в пределах 300 м — один терминал.
const collapseMaxM = 300

func CollapseStopArea(records []model.AdaptedRecord) []model.AdaptedRecord {
	byName := map[string][]int{}
	order := []string{}
	for i, r := range records {
		key := namesim.Core(r.NameRu)
		if _, ok := byName[key]; !ok {
			order = append(order, key)
		}
		byName[key] = append(byName[key], i)
	}
	out := make([]model.AdaptedRecord, 0, len(byName))
	for _, key := range order {
		out = append(out, collapseNameGroup(records, byName[key])...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NameRu != out[j].NameRu {
			return out[i].NameRu < out[j].NameRu
		}
		return out[i].PrimaryCode() < out[j].PrimaryCode()
	})
	return out
}

func collapseNameGroup(records []model.AdaptedRecord, idx []int) []model.AdaptedRecord {
	var geo, nogeom []int
	for _, i := range idx {
		if records[i].HasCoords() {
			geo = append(geo, i)
		} else {
			nogeom = append(nogeom, i)
		}
	}
	var out []model.AdaptedRecord
	for _, cluster := range clusterByDistance(records, geo, collapseMaxM) {
		base := records[cluster[0]]
		for _, j := range cluster[1:] {
			base = mergeInto(base, records[j])
		}
		out = append(out, base)
	}
	if len(nogeom) > 0 {
		base := records[nogeom[0]]
		for _, j := range nogeom[1:] {
			base = mergeInto(base, records[j])
		}
		out = append(out, base)
	}
	return out
}

func clusterByDistance(records []model.AdaptedRecord, idx []int, maxM float64) [][]int {
	parent := map[int]int{}
	for _, i := range idx {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for a := 0; a < len(idx); a++ {
		for b := a + 1; b < len(idx); b++ {
			i, j := idx[a], idx[b]
			d := haversineM(*records[i].Lat, *records[i].Lon, *records[j].Lat, *records[j].Lon)
			if d <= maxM {
				ri, rj := find(i), find(j)
				if ri != rj {
					if ri < rj {
						parent[rj] = ri
					} else {
						parent[ri] = rj
					}
				}
			}
		}
	}
	byRoot := map[int][]int{}
	roots := []int{}
	for _, i := range idx {
		r := find(i)
		if _, ok := byRoot[r]; !ok {
			roots = append(roots, r)
		}
		byRoot[r] = append(byRoot[r], i)
	}
	sort.Ints(roots)
	clusters := make([][]int, 0, len(roots))
	for _, r := range roots {
		members := byRoot[r]
		sort.Ints(members)
		clusters = append(clusters, members)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i][0] < clusters[j][0] })
	return clusters
}

func mergeInto(base, other model.AdaptedRecord) model.AdaptedRecord {
	bt, ot := extra(base, "transport_type"), extra(other, "transport_type")
	if ot != "" && !transportContains(bt, ot) {
		if bt == "" {
			base = withExtra(base, "transport_type", ot)
		} else {
			base = withExtra(base, "transport_type", normalizeTransport(bt+"+"+ot))
		}
	}
	seen := map[string]bool{}
	for _, id := range base.Identifiers {
		seen[id.System+"|"+id.CodeType+"|"+id.Code] = true
	}
	for _, id := range other.Identifiers {
		k := id.System + "|" + id.CodeType + "|" + id.Code
		if !seen[k] {
			base.Identifiers = append(base.Identifiers, id)
			seen[k] = true
		}
	}
	if !base.HasCoords() && other.HasCoords() {
		base.Lat, base.Lon = other.Lat, other.Lon
	}
	return base
}

func transportContains(combined, single string) bool {
	if combined == single {
		return true
	}
	for _, x := range splitPlus(combined) {
		if x == single {
			return true
		}
	}
	return false
}

func haversineM(aLat, aLon, bLat, bLon float64) float64 {
	return geo.Haversine(
		model.Coords{Lat: aLat, Lon: aLon},
		model.Coords{Lat: bLat, Lon: bLon},
	) * 1000
}

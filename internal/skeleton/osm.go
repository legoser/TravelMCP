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
		return "rail"
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

func CollapseStopArea(records []model.AdaptedRecord) []model.AdaptedRecord {
	groups := map[string][]int{}
	order := []string{}
	for i, r := range records {
		key := stopAreaKey(r)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	out := make([]model.AdaptedRecord, 0, len(groups))
	for _, key := range order {
		idx := groups[key]
		base := records[idx[0]]
		for _, j := range idx[1:] {
			other := records[j]
			base = mergeInto(base, other)
		}
		out = append(out, base)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NameRu != out[j].NameRu {
			return out[i].NameRu < out[j].NameRu
		}
		return out[i].PrimaryCode() < out[j].PrimaryCode()
	})
	return out
}

func mergeInto(base, other model.AdaptedRecord) model.AdaptedRecord {
	bt, ot := extra(base, "transport_type"), extra(other, "transport_type")
	if ot != "" && !transportContains(bt, ot) {
		if bt == "" {
			base = withExtra(base, "transport_type", ot)
		} else {
			base = withExtra(base, "transport_type", bt+"+"+ot)
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

func stopAreaKey(r model.AdaptedRecord) string {
	core := namesim.Core(r.NameRu)
	if r.HasCoords() {
		return core + "|" + geoCell(*r.Lat, *r.Lon)
	}
	return core + "|nogeom"
}

func geoCell(lat, lon float64) string {
	return fmt.Sprintf("%d:%d", int(lat*100), int(lon*100))
}

func haversineM(aLat, aLon, bLat, bLon float64) float64 {
	return geo.Haversine(
		model.Coords{Lat: aLat, Lon: aLon},
		model.Coords{Lat: bLat, Lon: bLon},
	) * 1000
}

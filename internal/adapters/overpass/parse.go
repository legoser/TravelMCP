package overpass

import (
	"encoding/json"
	"fmt"
	"strings"

	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

// ParseResponse converts a raw Overpass JSON response into terminal records.
// Elements without a name or without coordinates are skipped: they cannot
// participate in ScorePair verification (no identity or no geometry).
func ParseResponse(body []byte) ([]model.AdaptedRecord, error) {
	var data overpassResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("overpass parse: %w", err)
	}
	records := make([]model.AdaptedRecord, 0, len(data.Elements))
	for _, el := range data.Elements {
		rec, ok := elementToRecord(el)
		if !ok {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

func elementToRecord(el overpassElement) (model.AdaptedRecord, bool) {
	name := strings.TrimSpace(el.Tags["name"])
	if name == "" {
		return model.AdaptedRecord{}, false
	}
	var lat, lon *float64
	switch {
	case el.Lat != nil && el.Lon != nil:
		la, lo := *el.Lat, *el.Lon
		lat, lon = &la, &lo
	case el.Center != nil:
		la, lo := el.Center.Lat, el.Center.Lon
		lat, lon = &la, &lo
	default:
		return model.AdaptedRecord{}, false
	}
	code := fmt.Sprintf("%d", el.ID)
	if el.Type != "" && el.Type != "node" {
		code = el.Type + "/" + code
	}
	kind := el.Type
	if kind == "" {
		kind = "node"
	}
	return model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: name,
		NameEn: strings.TrimSpace(el.Tags["name:en"]),
		Lat:    lat,
		Lon:    lon,
		Source: "osm",
		Identifiers: []model.AdaptedIdentifier{
			{System: "osm", CodeType: "osm_id", Code: code},
		},
		Extra: map[string]string{
			"settlement":     namesim.ExtractSettlement(name),
			"transport_type": overpassTransportType(el.Tags),
			"object_type":    overpassObjectType(el.Tags),
			"osm_kind":       kind,
		},
	}, true
}

func overpassTransportType(tags map[string]string) string {
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

func overpassObjectType(tags map[string]string) string {
	if v, ok := tags["public_transport"]; ok && v == "station" {
		return "station"
	}
	if v, ok := tags["amenity"]; ok && v == "bus_station" {
		return "station"
	}
	if v, ok := tags["highway"]; ok && v == "bus_station" {
		return "station"
	}
	return "stop"
}

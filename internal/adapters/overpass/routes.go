package overpass

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
)

type routeMember struct {
	Type string `json:"type"`
	Ref  int64  `json:"ref"`
	Role string `json:"role"`
}

type routeRelation struct {
	Type    string            `json:"type"`
	ID      int64             `json:"id"`
	Members []routeMember     `json:"members"`
	Tags    map[string]string `json:"tags"`
}

type routeResponse struct {
	Elements []routeRelation `json:"elements"`
}

type AdaptedTripStop struct {
	OSMID int64   `json:"osm_id"`
	Lat   float64 `json:"lat,omitempty"`
	Lon   float64 `json:"lon,omitempty"`
	Role  string  `json:"role"`
	Name  string  `json:"name,omitempty"`
}

type AdaptedTripData struct {
	Ref      string            `json:"ref"`
	Operator string            `json:"operator,omitempty"`
	Network  string            `json:"network,omitempty"`
	Stops    []AdaptedTripStop `json:"stops"`
}

func BuildRouteQuery(ref string, bbox *BBox, timeoutS int) string {
	if timeoutS <= 0 {
		timeoutS = 60
	}
	ref = EscapeQLString(strings.TrimSpace(ref))
	bboxFilter := ""
	if bbox != nil {
		bboxFilter = fmt.Sprintf("(%s)", bbox.String())
	}
	return fmt.Sprintf(`[out:json][timeout:%d];
relation["type"="route"]["route"~"bus|trolleybus"]["ref"="%s"]%s;
out body;
>;
out skel qt;`,
		timeoutS, ref, bboxFilter)
}

// RouteRefBBox — параметры route-запроса как ключ кэша (O-5): ref + bbox
// идентифицируют запрос детерминированно; копия значений, не указатель.
type RouteQuery struct {
	Ref  string
	BBox *BBox
}

// FetchRouteRelations — O-5: маршрутные relation'ы Overpass (identity-доказательство
// маршрута: ref/operator/network + упорядоченные члены-остановки; времена —
// монополия Яндекса, §1.2). Сеть — через execQL (main→mirror fallback уже в
// адаптере); путь cache+quota — в geocoder.CachedRoutesProvider, не здесь.
func (a *Adapter) FetchRouteRelations(ctx context.Context, q RouteQuery) ([]model.AdaptedRecord, error) {
	ql := BuildRouteQuery(q.Ref, q.BBox, 0)
	body, err := a.execQL(ctx, ql)
	if err != nil {
		return nil, err
	}
	return ParseRouteResponse(body)
}

// FetchRoutes — контракт geocoder.OverpassRoutesProvider для пути cache+quota:
// мост обезличенных RouteQueryParams → RouteQuery адаптера.
func (a *Adapter) FetchRoutes(ctx context.Context, q geocoder.RouteQueryParams) ([]model.AdaptedRecord, error) {
	rq := RouteQuery{Ref: q.Ref}
	if q.HasBBox {
		rq.BBox = &BBox{MinLat: q.MinLat, MinLon: q.MinLon, MaxLat: q.MaxLat, MaxLon: q.MaxLon}
	}
	return a.FetchRouteRelations(ctx, rq)
}

func ParseRouteResponse(body []byte) ([]model.AdaptedRecord, error) {
	var data routeResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("overpass route parse: %w", err)
	}
	records := make([]model.AdaptedRecord, 0, len(data.Elements))
	for _, rel := range data.Elements {
		rec, ok := relationToRecord(rel)
		if !ok {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

func relationToRecord(rel routeRelation) (model.AdaptedRecord, bool) {
	ref := strings.TrimSpace(rel.Tags["ref"])
	if ref == "" {
		return model.AdaptedRecord{}, false
	}
	name := strings.TrimSpace(rel.Tags["name"])
	if name == "" {
		return model.AdaptedRecord{}, false
	}

	stops := make([]AdaptedTripStop, 0, len(rel.Members))
	for _, m := range rel.Members {
		if m.Type != "node" && m.Type != "way" {
			continue
		}
		role := m.Role
		if role == "" {
			role = "stop"
		}
		stops = append(stops, AdaptedTripStop{
			OSMID: m.Ref,
			Role:  role,
		})
	}

	tripData := AdaptedTripData{
		Ref:      ref,
		Operator: strings.TrimSpace(rel.Tags["operator"]),
		Network:  strings.TrimSpace(rel.Tags["network"]),
		Stops:    stops,
	}
	raw, _ := json.Marshal(tripData)

	identifiers := []model.AdaptedIdentifier{
		{System: "osm", CodeType: "osm_id", Code: fmt.Sprintf("relation/%d", rel.ID)},
	}
	if ref != "" {
		identifiers = append(identifiers, model.AdaptedIdentifier{
			System:   "osm",
			CodeType: "route_ref",
			Code:     ref,
		})
	}

	return model.AdaptedRecord{
		Kind:        model.AdaptedTrip,
		Identifiers: identifiers,
		NameRu:      strings.TrimSpace(rel.Tags["name"]),
		Source:      "osm",
		Raw:         raw,
		Extra: map[string]string{
			"operator":   strings.TrimSpace(rel.Tags["operator"]),
			"network":    strings.TrimSpace(rel.Tags["network"]),
			"route_type": strings.TrimSpace(rel.Tags["route"]),
			"stop_count": fmt.Sprintf("%d", len(stops)),
		},
	}, true
}

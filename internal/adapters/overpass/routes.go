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

// routeElement — элемент ответа Overpass в единой форме: relation несёт
// members+tags, node — lat/lon (+tags при out body), way — список nodes.
type routeElement struct {
	Type    string            `json:"type"`
	ID      int64             `json:"id"`
	Lat     *float64          `json:"lat,omitempty"`
	Lon     *float64          `json:"lon,omitempty"`
	Nodes   []int64           `json:"nodes,omitempty"`
	Members []routeMember     `json:"members,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
}

type routeResponse struct {
	Elements []routeElement `json:"elements"`
}

// elementKey/routeIndex: id нод, way'ов и relation'ов живут в разных
// пространствах чисел OSM, ключ обязан различать типы.
type elementKey struct {
	Type string
	ID   int64
}

type routeIndex map[elementKey]*routeElement

func (idx routeIndex) element(typ string, id int64) *routeElement {
	return idx[elementKey{Type: typ, ID: id}]
}

func (idx routeIndex) nodeCoord(id int64) (float64, float64, bool) {
	el := idx.element("node", id)
	if el == nil || el.Lat == nil || el.Lon == nil {
		return 0, 0, false
	}
	return *el.Lat, *el.Lon, true
}

// elementCoords — точки, описывающие элемент: node — своя точка, way — его
// ноды, relation — его ноды-члены (платформа-отношение).
func (idx routeIndex) elementCoords(typ string, id int64) [][2]float64 {
	el := idx.element(typ, id)
	if el == nil {
		return nil
	}
	switch typ {
	case "node":
		if el.Lat != nil && el.Lon != nil {
			return [][2]float64{{*el.Lat, *el.Lon}}
		}
	case "way":
		out := make([][2]float64, 0, len(el.Nodes))
		for _, nid := range el.Nodes {
			if lat, lon, ok := idx.nodeCoord(nid); ok {
				out = append(out, [2]float64{lat, lon})
			}
		}
		return out
	case "relation":
		out := make([][2]float64, 0, len(el.Members))
		for _, m := range el.Members {
			if m.Type != "node" {
				continue
			}
			if lat, lon, ok := idx.nodeCoord(m.Ref); ok {
				out = append(out, [2]float64{lat, lon})
			}
		}
		return out
	}
	return nil
}

func centroid(coords [][2]float64) (float64, float64, bool) {
	if len(coords) == 0 {
		return 0, 0, false
	}
	var latSum, lonSum float64
	for _, c := range coords {
		latSum += c[0]
		lonSum += c[1]
	}
	n := float64(len(coords))
	return latSum / n, lonSum / n, true
}

// isStopRole — роли членов relation, означающие остановку (PTv2).
func isStopRole(role string) bool {
	switch role {
	case "stop", "stop_entry_only", "stop_exit_only",
		"platform", "platform_entry_only", "platform_exit_only":
		return true
	}
	return false
}

type AdaptedTripStop struct {
	OSMID int64   `json:"osm_id"`
	Lat   float64 `json:"lat,omitempty"`
	Lon   float64 `json:"lon,omitempty"`
	Role  string  `json:"role"`
	Name  string  `json:"name,omitempty"`
}

// AdaptedTripData — Raw-поле записи AdaptedTrip (O-5): порядок Stops —
// порядок членов relation (семантика маршрута, не пересортировывать);
// Geometry — полилиния [[lat,lon],...] из way-членов с ролями
// ""/forward/backward; сегменты при разрыве конкатенируются в порядке
// следования членов.
type AdaptedTripData struct {
	Ref      string            `json:"ref"`
	Operator string            `json:"operator,omitempty"`
	Network  string            `json:"network,omitempty"`
	Stops    []AdaptedTripStop `json:"stops"`
	Geometry [][2]float64      `json:"geometry,omitempty"`
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
	refFilter := fmt.Sprintf(`["ref"="%s"]`, ref)
	if ref == "" {
		// регио-сбор (issue #12): все маршрутные relation'ы bbox, фильтра
		// по ref нет — терминальные станции региона уже собраны, теперь
		// маршруты между ними.
		refFilter = ""
	}
	return fmt.Sprintf(`[out:json][timeout:%d];
relation["type"="route"]["route"~"bus|trolleybus"]%s%s;
out body;
>;
out skel qt;`,
		timeoutS, refFilter, bboxFilter)
}

// BuildRouteByIDQuery — маршрут по уникальному relation_id (точнее
// ref-поиска: нет совпадений бортовых номеров). out body, не skel:
// члены-ноды приходят с тегами → имена остановок; ноды-вершины геометрии
// тегов не несут, разницы в объёме нет.
func BuildRouteByIDQuery(relationID int64, timeoutS int) string {
	if timeoutS <= 0 {
		timeoutS = 60
	}
	return fmt.Sprintf(`[out:json][timeout:%d];
relation(%d);
out body;
>;
out qt;`, timeoutS, relationID)
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

// FetchAllRouteRelations — регио-сбор (issue #12): все маршрутные
// relation'ы bbox (без ref-фильтра). Терм — таймаут поднят: региона больше,
// чем у одного номера. Контракт OverpassRoutesSource для sync.
func (a *Adapter) FetchAllRouteRelations(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error) {
	ql := BuildRouteQuery("", &BBox{MinLat: minLat, MinLon: minLon, MaxLat: maxLat, MaxLon: maxLon}, 0)
	body, err := a.execQL(ctx, ql)
	if err != nil {
		return nil, err
	}
	return ParseRouteResponse(body)
}

// FetchRoute — O-5, завершение: маршрут по relation_id — упорядоченные
// остановки с координатами и именами + геометрия из way-членов. Путь
// cache+quota — geocoder.CachedRoutesProvider (RouteQueryParams.RelationID).
func (a *Adapter) FetchRoute(ctx context.Context, relationID int64) ([]model.AdaptedRecord, error) {
	if relationID <= 0 {
		return nil, fmt.Errorf("overpass: invalid relation id %d", relationID)
	}
	ql := BuildRouteByIDQuery(relationID, 0)
	body, err := a.execQL(ctx, ql)
	if err != nil {
		return nil, err
	}
	return ParseRouteResponse(body)
}

// FetchRoutes — контракт geocoder.OverpassRoutesProvider для пути cache+quota:
// мост обезличенных RouteQueryParams → запросы адаптера. RelationID
// приоритетнее Ref: точечный запрос по уникальному id.
func (a *Adapter) FetchRoutes(ctx context.Context, q geocoder.RouteQueryParams) ([]model.AdaptedRecord, error) {
	if q.RelationID > 0 {
		return a.FetchRoute(ctx, q.RelationID)
	}
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
	idx := make(routeIndex, len(data.Elements))
	for i := range data.Elements {
		if data.Elements[i].Type != "" {
			idx[elementKey{Type: data.Elements[i].Type, ID: data.Elements[i].ID}] = &data.Elements[i]
		}
	}
	records := make([]model.AdaptedRecord, 0)
	for i := range data.Elements {
		if data.Elements[i].Type != "relation" {
			continue
		}
		rec, ok := relationToRecord(&data.Elements[i], idx)
		if !ok {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

func relationToRecord(rel *routeElement, idx routeIndex) (model.AdaptedRecord, bool) {
	ref := strings.TrimSpace(rel.Tags["ref"])
	if ref == "" {
		return model.AdaptedRecord{}, false
	}
	name := strings.TrimSpace(rel.Tags["name"])
	if name == "" {
		return model.AdaptedRecord{}, false
	}

	stops := make([]AdaptedTripStop, 0, len(rel.Members))
	geoWays := make([]int64, 0, len(rel.Members))
	for _, m := range rel.Members {
		switch m.Type {
		case "node":
			role := m.Role
			if role == "" {
				role = "stop"
			}
			stop := AdaptedTripStop{OSMID: m.Ref, Role: role}
			if el := idx.element("node", m.Ref); el != nil {
				if el.Lat != nil && el.Lon != nil {
					stop.Lat, stop.Lon = *el.Lat, *el.Lon
				}
				stop.Name = strings.TrimSpace(el.Tags["name"])
			}
			stops = append(stops, stop)
		case "way", "relation":
			if !isStopRole(m.Role) {
				if m.Type == "way" {
					geoWays = append(geoWays, m.Ref)
				}
				continue
			}
			stop := AdaptedTripStop{OSMID: m.Ref, Role: m.Role}
			if lat, lon, ok := centroid(idx.elementCoords(m.Type, m.Ref)); ok {
				stop.Lat, stop.Lon = lat, lon
			}
			if el := idx.element(m.Type, m.Ref); el != nil {
				stop.Name = strings.TrimSpace(el.Tags["name"])
			}
			stops = append(stops, stop)
		}
	}

	tripData := AdaptedTripData{
		Ref:      ref,
		Operator: strings.TrimSpace(rel.Tags["operator"]),
		Network:  strings.TrimSpace(rel.Tags["network"]),
		Stops:    stops,
		Geometry: buildGeometry(geoWays, idx),
	}
	raw, _ := json.Marshal(tripData)

	identifiers := []model.AdaptedIdentifier{
		{System: "osm", CodeType: "osm_id", Code: fmt.Sprintf("relation/%d", rel.ID)},
	}
	identifiers = append(identifiers, model.AdaptedIdentifier{
		System:   "osm",
		CodeType: "route_ref",
		Code:     ref,
	})

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

// buildGeometry — склейка way-членов маршрута в полилинию [[lat,lon],...]:
// way стыкуется к концу цепочки напрямую (начало) или с разворотом (хвост);
// при разрыве сегменты конкатенируются в порядке следования членов. Ноды
// без координат пропускаются, но продолжают нести связность цепочки.
func buildGeometry(wayIDs []int64, idx routeIndex) [][2]float64 {
	var poly [][2]float64
	var lastNode int64
	haveLast := false
	appendNode := func(nid int64) {
		lastNode = nid
		haveLast = true
		if lat, lon, ok := idx.nodeCoord(nid); ok {
			poly = append(poly, [2]float64{lat, lon})
		}
	}
	for _, wid := range wayIDs {
		el := idx.element("way", wid)
		if el == nil || len(el.Nodes) == 0 {
			continue
		}
		nodes := el.Nodes
		switch {
		case !haveLast:
			for _, nid := range nodes {
				appendNode(nid)
			}
		case nodes[0] == lastNode:
			for _, nid := range nodes[1:] {
				appendNode(nid)
			}
		case nodes[len(nodes)-1] == lastNode:
			for i := len(nodes) - 2; i >= 0; i-- {
				appendNode(nodes[i])
			}
		default:
			for _, nid := range nodes {
				if nid == lastNode {
					continue
				}
				appendNode(nid)
			}
		}
	}
	return poly
}

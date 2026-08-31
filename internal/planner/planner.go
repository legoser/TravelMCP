package planner

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/telemetry"
)

type Planner struct {
	metrics *telemetry.Metrics
}

func New(metrics *telemetry.Metrics) *Planner {
	return &Planner{metrics: metrics}
}

func (p *Planner) Plan(net *model.Network, from, to model.Coords, params model.SearchParams) (*model.Journey, error) {
	return p.planWithStops(net, from, to, params, nil, nil)
}

// PlanWithPlaces планирует маршрут, разрешая названия мест в стопы напрямую.
// Если fromPlace/toPlace заданы, ищется стоп с точным совпадением имени/координат
// из газетира — это позволяет избежать «лишнего» пешего участка до ближайшего стопа.
func (p *Planner) PlanWithPlaces(net *model.Network, from, to model.Coords, params model.SearchParams, fromPlace, toPlace *PlaceHint) (*model.Journey, error) {
	return p.planWithStops(net, from, to, params, fromPlace, toPlace)
}

type PlaceHint struct {
	Name string
}

func (p *Planner) planWithStops(net *model.Network, from, to model.Coords, params model.SearchParams, fromPlace, toPlace *PlaceHint) (*model.Journey, error) {
	maxWalk := params.MaxWalkMinutes
	if maxWalk <= 0 {
		maxWalk = 30
	}

	var fromStop, toStop *model.Stop
	var foundFrom, foundTo bool

	if fromPlace != nil {
		fromStop, foundFrom = findStopByPlace(net.Stops, fromPlace.Name, from, maxWalk)
	}
	if !foundFrom {
		fromStops := geo.NearestStops(net.Stops, from, maxWalk, 1)
		if len(fromStops) == 0 {
			return nil, fmt.Errorf("planner: нет остановок, достижимых пешком (лимит %d мин) от точки отправления", maxWalk)
		}
		fromStop = fromStops[0]
	}

	if toPlace != nil {
		toStop, foundTo = findStopByPlace(net.Stops, toPlace.Name, to, maxWalk)
	}
	if !foundTo {
		toStops := geo.NearestStops(net.Stops, to, maxWalk, 1)
		if len(toStops) == 0 {
			return nil, fmt.Errorf("planner: нет остановок, достижимых пешком (лимит %d мин) от точки назначения", maxWalk)
		}
		toStop = toStops[0]
	}

	accessMin := geo.WalkTimeMinutes(geo.Haversine(from, fromStop.Coordinates()))
	egressMin := geo.WalkTimeMinutes(geo.Haversine(to, toStop.Coordinates()))

	journey := &model.Journey{
		From: from,
		To:   to,
	}
	journey.Legs = append(journey.Legs, model.Leg{
		Mode:      model.ModeWalk,
		From:      model.LegPoint{Name: "Точка отправления", Lat: from.Lat, Lon: from.Lon},
		To:        legPoint(fromStop),
		Departure: params.Departure,
		Arrival:   params.Departure.Add(time.Duration(accessMin) * time.Minute),
	})
	journey.Departure = params.Departure

	departAtStop := journey.Legs[len(journey.Legs)-1].Arrival

	transitLegs, err := p.csa(net, fromStop.ID, toStop.ID, departAtStop, params)
	if err != nil {
		return nil, err
	}
	journey.Legs = append(journey.Legs, transitLegs...)
	journey.Transfers = len(transitLegs) - 1

	transitEnd := transitLegs[len(transitLegs)-1].Arrival
	journey.Legs = append(journey.Legs, model.Leg{
		Mode:      model.ModeWalk,
		From:      legPoint(toStop),
		To:        model.LegPoint{Name: "Точка назначения", Lat: to.Lat, Lon: to.Lon},
		Departure: transitEnd,
		Arrival:   transitEnd.Add(time.Duration(egressMin) * time.Minute),
	})

	journey.Arrival = journey.Legs[len(journey.Legs)-1].Arrival

	if p.metrics != nil {
		p.metrics.Inc("planner.planned")
	}
	return journey, nil
}

func legPoint(stop *model.Stop) model.LegPoint {
	return model.LegPoint{StopID: stop.ID, Name: stop.Name, Lat: stop.Lat, Lon: stop.Lon}
}

// findStopByPlace ищет стоп, связанный с названием места.
// Сначала ищет по точному/частичному совпадению имени, затем по близости координат.
func findStopByPlace(stops map[string]*model.Stop, placeName string, coords model.Coords, maxWalkMinutes int) (*model.Stop, bool) {
	// 1. Ищем стоп с совпадающим или содержащим название
	var nameMatch *model.Stop
	var nameDist float64 = 1e9 // большой порог
	for _, s := range stops {
		if strings.Contains(strings.ToLower(s.Name), strings.ToLower(placeName)) ||
			strings.Contains(strings.ToLower(placeName), strings.ToLower(s.Name)) {
			d := geo.Haversine(coords, s.Coordinates())
			if d < nameDist {
				nameDist = d
				nameMatch = s
			}
		}
	}
	if nameMatch != nil {
		return nameMatch, true
	}

	// 2. Ищем ближайший стоп в пределах доступности
	best := geo.NearestStops(stops, coords, maxWalkMinutes, 1)
	if len(best) > 0 {
		return best[0], true
	}
	return nil, false
}

type prev struct {
	conn     *model.Connection
	footFrom string
}

type step struct {
	conn     *model.Connection
	footFrom string
	footTo   string
}

func (p *Planner) csa(net *model.Network, fromStop, toStop string, depart time.Time, params model.SearchParams) ([]model.Leg, error) {
	maxTransfers := params.MaxTransfers
	if maxTransfers < 0 {
		maxTransfers = -1
	}

	allowed := map[model.Mode]bool{}
	for _, m := range params.AllowedModes {
		allowed[m] = true
	}

	conns := make([]model.Connection, len(net.Connections))
	copy(conns, net.Connections)
	sort.Slice(conns, func(i, j int) bool {
		if conns[i].Departure.Equal(conns[j].Departure) {
			return conns[i].Arrival.Before(conns[j].Arrival)
		}
		return conns[i].Departure.Before(conns[j].Departure)
	})

	transfersFrom := map[string][]model.Transfer{}
	for _, tr := range net.Transfers {
		transfersFrom[tr.FromStopID] = append(transfersFrom[tr.FromStopID], tr)
	}

	arr := map[string]time.Time{fromStop: depart}
	pred := map[string]*prev{}

	relax := func(stopID string) {
		queue := []string{stopID}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			curT := arr[cur]
			for _, tr := range transfersFrom[cur] {
				cand := curT.Add(time.Duration(tr.Minutes) * time.Minute)
				if at, ok := arr[tr.ToStopID]; !ok || cand.Before(at) {
					arr[tr.ToStopID] = cand
					pred[tr.ToStopID] = &prev{footFrom: cur}
					queue = append(queue, tr.ToStopID)
				}
			}
		}
	}
	relax(fromStop)

	for i := range conns {
		c := &conns[i]
		if len(allowed) > 0 && !allowed[c.Mode] {
			continue
		}
		atFrom, ok := arr[c.From]
		if !ok {
			continue
		}
		if c.Departure.Before(atFrom) {
			continue
		}
		if atTo, ok := arr[c.To]; ok && !c.Arrival.Before(atTo) {
			continue
		}
		arr[c.To] = c.Arrival
		pred[c.To] = &prev{conn: c}
		relax(c.To)
	}

	if pred[toStop] == nil {
		return nil, fmt.Errorf("planner: маршрут между %s и %s не найден (нет покрытия или расписаний)", fromStop, toStop)
	}

	steps := p.reconstruct(pred, fromStop, toStop)
	legs := buildLegs(net, arr, steps)

	if maxTransfers > 0 && len(legs)-1 > maxTransfers {
		return nil, fmt.Errorf("planner: маршрут требует %d пересадок, больше лимита %d", len(legs)-1, maxTransfers)
	}
	if maxTransfers == 0 && len(legs)-1 > 0 {
		return nil, fmt.Errorf("planner: маршрут требует пересадок, а лимит — без пересадок")
	}
	return legs, nil
}

func (p *Planner) reconstruct(pred map[string]*prev, fromStop, toStop string) []step {
	var rev []step
	stop := toStop
	for stop != fromStop {
		pr := pred[stop]
		if pr == nil {
			break
		}
		if pr.conn != nil {
			rev = append(rev, step{conn: pr.conn})
			stop = pr.conn.From
		} else {
			rev = append(rev, step{footFrom: pr.footFrom, footTo: stop})
			stop = pr.footFrom
		}
	}
	steps := make([]step, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		steps = append(steps, rev[i])
	}
	return steps
}

func buildLegs(net *model.Network, arr map[string]time.Time, steps []step) []model.Leg {
	var legs []model.Leg
	var cur *model.Leg

	endLeg := func() {
		if cur != nil {
			legs = append(legs, *cur)
			cur = nil
		}
	}

	for _, st := range steps {
		if st.conn != nil {
			c := st.conn
			from := net.Stops[c.From]
			to := net.Stops[c.To]
			if cur != nil && cur.TripID != "" && cur.TripID == c.TripID {
				cur.To = legPoint(to)
				cur.Arrival = c.Arrival
				continue
			}
			endLeg()
			leg := &model.Leg{
				Mode:       c.Mode,
				ProviderID: c.ProviderID,
				RouteID:    c.RouteID,
				TripID:     c.TripID,
				From:       legPoint(from),
				To:         legPoint(to),
				Departure:  c.Departure,
				Arrival:    c.Arrival,
			}
			cur = leg
			continue
		}
		endLeg()
		from := net.Stops[st.footFrom]
		to := net.Stops[st.footTo]
		dep := arr[st.footFrom]
		ar := arr[st.footTo]
		legs = append(legs, model.Leg{
			Mode:       model.ModeWalk,
			ProviderID: from.ProviderID,
			From:       legPoint(from),
			To:         legPoint(to),
			Departure:  dep,
			Arrival:    ar,
		})
	}
	endLeg()
	return legs
}

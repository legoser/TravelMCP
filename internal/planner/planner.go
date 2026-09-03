package planner

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/telemetry"
)

type Planner struct {
	metrics *telemetry.Metrics
	engine  string
	logger  *slog.Logger
	sem     chan struct{}
}

func New(metrics *telemetry.Metrics) *Planner {
	return NewWithConfig(metrics, "csa", nil, 0, true)
}

func NewWithEngine(metrics *telemetry.Metrics, engine string) *Planner {
	return NewWithConfig(metrics, engine, nil, 0, true)
}

func NewWithLogger(metrics *telemetry.Metrics, engine string, logger *slog.Logger) *Planner {
	return NewWithConfig(metrics, engine, logger, 0, true)
}

func NewWithConfig(metrics *telemetry.Metrics, engine string, logger *slog.Logger, semSize int, semEnable bool) *Planner {
	if engine == "" {
		engine = "csa"
	}
	if semSize <= 0 {
		semSize = runtime.NumCPU() * 2
	}
	var sem chan struct{}
	if semEnable {
		sem = make(chan struct{}, semSize)
	}
	return &Planner{metrics: metrics, engine: engine, logger: logger, sem: sem}
}

func (p *Planner) acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Planner) release() { <-p.sem }

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

func isVillageStop(s *model.Stop) bool {
	if s == nil {
		return false
	}
	return model.IsVillageName(s.Name)
}

func speedLimit(m model.Mode) float64 {
	return model.MaxSpeed(m)
}

func isImplausibleConnection(c *model.Connection, net *model.Network) bool {
	fromS, ok1 := net.Stops[c.From]
	toS, ok2 := net.Stops[c.To]
	if !ok1 || !ok2 {
		return false
	}
	if (fromS.Lat == 0 && fromS.Lon == 0) || (toS.Lat == 0 && toS.Lon == 0) {
		return false
	}
	dist := geo.Haversine(fromS.Coordinates(), toS.Coordinates())
	dur := c.Arrival.Sub(c.Departure).Minutes()
	if dur <= 0 || dist < 5 {
		return false
	}
	speed := dist / (dur / 60)
	return speed > speedLimit(c.Mode)
}

func isImplausibleLeg(fromS, toS *model.Stop, dep, arr time.Time, mode model.Mode) bool {
	if fromS == nil || toS == nil {
		return false
	}
	if (fromS.Lat == 0 && fromS.Lon == 0) || (toS.Lat == 0 && toS.Lon == 0) {
		return false
	}
	dist := geo.Haversine(fromS.Coordinates(), toS.Coordinates())
	dur := arr.Sub(dep).Minutes()
	if dur <= 0 || dist < 5 {
		return false
	}
	speed := dist / (dur / 60)
	return speed > speedLimit(mode)
}

func chooseBestStop(candidates []*model.Stop, origin map[string]bool) *model.Stop {
	if len(candidates) == 0 {
		return nil
	}
	for _, s := range candidates {
		if origin != nil && origin[s.ID] && !isVillageStop(s) {
			return s
		}
	}
	for _, s := range candidates {
		if origin != nil && origin[s.ID] {
			return s
		}
	}
	for _, s := range candidates {
		if !isVillageStop(s) {
			return s
		}
	}
	return candidates[0]
}

func chooseBestToStop(candidates []*model.Stop) *model.Stop {
	if len(candidates) == 0 {
		return nil
	}
	for _, s := range candidates {
		if !isVillageStop(s) {
			return s
		}
	}
	return candidates[0]
}

func (p *Planner) planWithStops(net *model.Network, from, to model.Coords, params model.SearchParams, fromPlace, toPlace *PlaceHint) (*model.Journey, error) {
	start := time.Now()
	defer func() { telemetry.ObservePlanner(p.engine, time.Since(start)) }()
	if p.sem != nil {
		if err := p.acquire(context.Background()); err != nil {
			return nil, err
		}
		defer p.release()
	}
	maxWalk := params.MaxWalkMinutes
	if maxWalk <= 0 {
		maxWalk = 30
	}
	if p.logger != nil {
		p.logger.Debug("plan start", "from", from, "to", to, "departure", params.Departure, "arrival", params.Arrival, "engine", p.engine, "maxWalk", maxWalk, "allowGap", params.AllowGap)
		p.logger.Debug("network", "stops", len(net.Stops), "trips", len(net.Trips), "connections", len(net.Connections), "transfers", len(net.Transfers))
	}

	var fromStop, toStop *model.Stop
	var foundFrom, foundTo bool

	originStops := map[string]bool{}
	for _, trip := range net.Trips {
		if len(trip.StopTimes) > 0 {
			originStops[trip.StopTimes[0].StopID] = true
		}
	}

	if fromPlace != nil {
		fromStop, foundFrom = findStopByPlace(net.Stops, fromPlace.Name, from, maxWalk, originStops)
	}
	idx := geo.NewSpatialIndex(net.Stops)
	if !foundFrom {
		fromStops := idx.Nearest(from, maxWalk, 0)
		if len(fromStops) == 0 {
			fromStops = geo.NearestStops(net.Stops, from, maxWalk, 0)
		}
		if len(fromStops) == 0 {
			return nil, fmt.Errorf("planner: нет остановок, достижимых пешком (лимит %d мин) от точки отправления", maxWalk)
		}
		fromStop = chooseBestStop(fromStops, originStops)
		if fromStop == nil {
			fromStop = fromStops[0]
		}
	}

	if toPlace != nil {
		toStop, foundTo = findStopByPlace(net.Stops, toPlace.Name, to, maxWalk, nil)
	}
	if !foundTo {
		toStops := idx.Nearest(to, maxWalk, 0)
		if len(toStops) == 0 {
			toStops = geo.NearestStops(net.Stops, to, maxWalk, 0)
		}
		if len(toStops) == 0 {
			return nil, fmt.Errorf("planner: нет остановок, достижимых пешком (лимит %d мин) от точки назначения", maxWalk)
		}
		if best := chooseBestToStop(toStops); best != nil {
			toStop = best
		} else {
			toStop = toStops[0]
		}
	}

	accessMin := geo.WalkTimeMinutes(geo.Haversine(from, fromStop.Coordinates()))
	egressMin := geo.WalkTimeMinutes(geo.Haversine(to, toStop.Coordinates()))

	if params.Arrival != nil {
		latestArrivalAtStop := params.Arrival.Add(-time.Duration(egressMin) * time.Minute)
		transitLegs, departAtStop, err := p.planArrival(net, fromStop.ID, toStop.ID, *params.Arrival, latestArrivalAtStop, params)
		if err != nil {
			if !params.AllowGap {
				return nil, err
			}
			distKm := geo.Haversine(fromStop.Coordinates(), toStop.Coordinates())
			mins := geo.WalkTimeMinutes(distKm)
			departAtStop = latestArrivalAtStop.Add(-time.Duration(mins) * time.Minute)
			transitLegs = []model.Leg{{
				Mode: model.ModeWalk, ProviderID: fromStop.ProviderID,
				From: legPoint(fromStop), To: legPoint(toStop),
				Departure: departAtStop, Arrival: latestArrivalAtStop,
				SelfProvided: true,
			}}
		}
		journey := &model.Journey{From: from, To: to}
		journey.Legs = append(journey.Legs, model.Leg{
			Mode: model.ModeWalk, From: model.LegPoint{Name: "Точка отправления", Lat: from.Lat, Lon: from.Lon},
			To: legPoint(fromStop), Departure: departAtStop.Add(-time.Duration(accessMin) * time.Minute), Arrival: departAtStop,
		})
		journey.Legs = append(journey.Legs, transitLegs...)
		journey.Transfers = len(transitLegs) - 1
		transitEnd := transitLegs[len(transitLegs)-1].Arrival
		journey.Legs = append(journey.Legs, model.Leg{
			Mode: model.ModeWalk, From: legPoint(toStop), To: model.LegPoint{Name: "Точка назначения", Lat: to.Lat, Lon: to.Lon},
			Departure: transitEnd, Arrival: transitEnd.Add(time.Duration(egressMin) * time.Minute),
		})
		journey.Departure = journey.Legs[0].Departure
		journey.Arrival = journey.Legs[len(journey.Legs)-1].Arrival
		journey.Alternatives = p.paretoAlternatives(net, from, to, params, fromStop, toStop, journey)
		pref := params.Preference
		if pref == "" {
			pref = model.PreferenceTransfers
		}
		if pref == model.PreferenceTransfers && len(journey.Alternatives) > 0 {
			bestIdx := -1
			bestTransfers := journey.Transfers
			for i, alt := range journey.Alternatives {
				if alt.Transfers < bestTransfers || (alt.Transfers == bestTransfers && alt.Arrival.Before(journey.Arrival)) {
					bestTransfers = alt.Transfers
					bestIdx = i
				}
			}
			if bestIdx >= 0 {
				newBest := journey.Alternatives[bestIdx]
				newAlts := []model.Journey{*journey}
				for i, a := range journey.Alternatives {
					if i != bestIdx {
						newAlts = append(newAlts, a)
					}
				}
				if len(newAlts) > 2 {
					newAlts = newAlts[:2]
				}
				journey = &newBest
				journey.Alternatives = newAlts
			}
		}
		if p.metrics != nil {
			p.metrics.Inc("planner.planned")
		}
		return journey, nil
	}

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

	var transitLegs []model.Leg
	var err error
	if p.engine == "raptor" {
		transitLegs, err = p.raptor(net, fromStop.ID, toStop.ID, departAtStop, params)
	} else {
		transitLegs, err = p.csa(net, fromStop.ID, toStop.ID, departAtStop, params)
	}
	if err != nil {
		if !params.AllowGap {
			return nil, err
		}
		distKm := geo.Haversine(fromStop.Coordinates(), toStop.Coordinates())
		mins := geo.WalkTimeMinutes(distKm)
		gapLeg := model.Leg{
			Mode: model.ModeWalk, ProviderID: fromStop.ProviderID,
			From: legPoint(fromStop), To: legPoint(toStop),
			Departure: departAtStop, Arrival: departAtStop.Add(time.Duration(mins) * time.Minute),
			SelfProvided: true,
		}
		transitLegs = []model.Leg{gapLeg}
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
	journey.Alternatives = p.paretoAlternatives(net, from, to, params, fromStop, toStop, journey)
	pref := params.Preference
	if pref == "" {
		pref = model.PreferenceTransfers
	}
	if pref == model.PreferenceTransfers && len(journey.Alternatives) > 0 {
		bestIdx := -1
		bestTransfers := journey.Transfers
		for i, alt := range journey.Alternatives {
			if alt.Transfers < bestTransfers || (alt.Transfers == bestTransfers && alt.Arrival.Before(journey.Arrival)) {
				bestTransfers = alt.Transfers
				bestIdx = i
			}
		}
		if bestIdx >= 0 {
			newBest := journey.Alternatives[bestIdx]
			newAlts := []model.Journey{*journey}
			for i, a := range journey.Alternatives {
				if i != bestIdx {
					newAlts = append(newAlts, a)
				}
			}
			if len(newAlts) > 2 {
				newAlts = newAlts[:2]
			}
			journey = &newBest
			journey.Alternatives = newAlts
		}
	}

	if p.metrics != nil {
		p.metrics.Inc("planner.planned")
	}
	return journey, nil
}

func (p *Planner) paretoAlternatives(net *model.Network, from, to model.Coords, params model.SearchParams, fromStop, toStop *model.Stop, best *model.Journey) []model.Journey {
	candidates := []*model.Journey{best}
	// Generate alternative departures within 6h window
	for offset := 60; offset <= 360; offset += 60 {
		var candParams model.SearchParams = params
		if params.Arrival != nil {
			altArrival := params.Arrival.Add(time.Duration(offset) * time.Minute)
			candParams.Arrival = &altArrival
		} else {
			candParams.Departure = params.Departure.Add(time.Duration(offset) * time.Minute)
		}
		// Run single journey without Pareto to avoid recursion: use raw csa/raptor
		accessMin := geo.WalkTimeMinutes(geo.Haversine(from, fromStop.Coordinates()))
		egressMin := geo.WalkTimeMinutes(geo.Haversine(to, toStop.Coordinates()))
		var legs []model.Leg
		var err error
		if candParams.Arrival != nil {
			latest := candParams.Arrival.Add(-time.Duration(egressMin) * time.Minute)
			var dep time.Time
			legs, dep, err = p.planArrival(net, fromStop.ID, toStop.ID, *candParams.Arrival, latest, candParams)
			if err != nil {
				continue
			}
			_ = dep
			_ = accessMin
		} else {
			depAtStop := candParams.Departure.Add(time.Duration(accessMin) * time.Minute)
			if p.engine == "raptor" {
				legs, err = p.raptor(net, fromStop.ID, toStop.ID, depAtStop, candParams)
			} else {
				legs, err = p.csa(net, fromStop.ID, toStop.ID, depAtStop, candParams)
			}
			if err != nil {
				continue
			}
		}
		if len(legs) == 0 {
			continue
		}
		j := &model.Journey{From: from, To: to, Legs: legs, Transfers: len(legs) - 1}
		// Estimate departure/arrival from legs
		if len(legs) > 0 {
			j.Departure = legs[0].Departure.Add(-time.Duration(accessMin) * time.Minute)
			j.Arrival = legs[len(legs)-1].Arrival.Add(time.Duration(egressMin) * time.Minute)
		}
		candidates = append(candidates, j)
	}
	// Deduplicate by arrival+transfers
	uniq := map[string]*model.Journey{}
	for _, c := range candidates {
		key := c.Arrival.Format(time.RFC3339) + fmt.Sprintf("-%d", c.Transfers)
		if _, ok := uniq[key]; !ok {
			uniq[key] = c
		}
	}
	list := make([]*model.Journey, 0, len(uniq))
	for _, v := range uniq {
		list = append(list, v)
	}
	pref := params.Preference
	if pref == "" {
		pref = model.PreferenceTransfers
	}
	if pref == model.PreferenceTransfers {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Transfers != list[j].Transfers {
				return list[i].Transfers < list[j].Transfers
			}
			return list[i].Arrival.Before(list[j].Arrival)
		})
	} else {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Arrival.Equal(list[j].Arrival) {
				return list[i].Transfers < list[j].Transfers
			}
			return list[i].Arrival.Before(list[j].Arrival)
		})
	}
	// Keep top 3 by arrival as Pareto front for now (ensure alternatives exist)
	if len(list) > 3 {
		list = list[:3]
	}
	var alts []model.Journey
	for _, f := range list {
		if f.Arrival.Equal(best.Arrival) && f.Transfers == best.Transfers {
			continue
		}
		alts = append(alts, *f)
		if len(alts) >= 2 {
			break
		}
	}
	return alts
}

func legPoint(stop *model.Stop) model.LegPoint {
	return model.LegPoint{StopID: stop.ID, Name: stop.Name, Lat: stop.Lat, Lon: stop.Lon}
}

// findStopByPlace ищет стоп, связанный с названием места.
// Сначала ищет по точному/частичному совпадению имени, затем по близости координат.
// Среди подходящих остановок предпочтение отдаётся автовокзалу (терминалу
// отправления), затем терминалу отправления рейса (origin), и лишь затем —
// «городской»/промежуточной остановке (например, вокзалу ЖД).
// Деревенские остановки (ОП, пов.) штрафуются: среди равных по смыслу
// предпочитаются не-деревенские, а деревня выбирается только если
// альтернатив в радиусе пешей доступности нет.
func findStopByPlace(stops map[string]*model.Stop, placeName string, coords model.Coords, maxWalkMinutes int, origin map[string]bool) (*model.Stop, bool) {
	lowerPlace := strings.ToLower(placeName)

	var nameMatch, nameAV, nameOrigin *model.Stop
	var nameAVNonVillage, nameOriginNonVillage *model.Stop
	nameDist := 1e9
	avDist, originDist := 1e9, 1e9
	avNonVillageDist, originNonVillageDist := 1e9, 1e9
	for _, s := range stops {
		if !(strings.Contains(strings.ToLower(s.Name), lowerPlace) ||
			strings.Contains(lowerPlace, strings.ToLower(s.Name))) {
			continue
		}
		d := geo.Haversine(coords, s.Coordinates())
		if s.IsHub() && d < avDist {
			avDist = d
			nameAV = s
		}
		if s.IsHub() && !isVillageStop(s) && d < avNonVillageDist {
			avNonVillageDist = d
			nameAVNonVillage = s
		}
		if origin != nil && origin[s.ID] && d < originDist {
			originDist = d
			nameOrigin = s
		}
		if origin != nil && origin[s.ID] && !isVillageStop(s) && d < originNonVillageDist {
			originNonVillageDist = d
			nameOriginNonVillage = s
		}
		if d < nameDist {
			nameDist = d
			nameMatch = s
		}
	}

	nearestWith := func(pred func(*model.Stop) bool) *model.Stop {
		for _, s := range geo.NearestStops(stops, coords, maxWalkMinutes, 0) {
			if pred(s) {
				return s
			}
		}
		return nil
	}

	if nameMatch != nil {
		if nameAVNonVillage != nil {
			return nameAVNonVillage, true
		}
		if nameOriginNonVillage != nil {
			return nameOriginNonVillage, true
		}
		if nameAV != nil {
			if av := nearestWith(func(s *model.Stop) bool { return s.IsHub() && !isVillageStop(s) }); av != nil {
				return av, true
			}
			return nameAV, true
		}
		if nameOrigin != nil {
			if o := nearestWith(func(s *model.Stop) bool { return origin[s.ID] && !isVillageStop(s) }); o != nil {
				return o, true
			}
			return nameOrigin, true
		}
		if isVillageStop(nameMatch) {
			if av := nearestWith(func(s *model.Stop) bool { return s.IsHub() && !isVillageStop(s) }); av != nil {
				return av, true
			}
			if origin != nil {
				if o := nearestWith(func(s *model.Stop) bool { return origin[s.ID] && !isVillageStop(s) }); o != nil {
					return o, true
				}
			}
			for _, s := range geo.NearestStops(stops, coords, maxWalkMinutes, 0) {
				if !isVillageStop(s) {
					return s, true
				}
			}
		}
		if !nameMatch.IsHub() {
			if av := nearestWith(func(s *model.Stop) bool { return s.IsHub() && !isVillageStop(s) }); av != nil {
				return av, true
			}
			if av := nearestWith(func(s *model.Stop) bool { return s.IsHub() }); av != nil {
				return av, true
			}
		}
		if origin != nil && !origin[nameMatch.ID] {
			if o := nearestWith(func(s *model.Stop) bool { return origin[s.ID] && !isVillageStop(s) }); o != nil {
				return o, true
			}
			if o := nearestWith(func(s *model.Stop) bool { return origin[s.ID] }); o != nil {
				return o, true
			}
		}
		return nameMatch, true
	}

	best := geo.NearestStops(stops, coords, maxWalkMinutes, 0)
	if len(best) > 0 {
		if av := nearestWith(func(s *model.Stop) bool { return s.IsHub() && !isVillageStop(s) }); av != nil {
			return av, true
		}
		if av := nearestWith(func(s *model.Stop) bool { return s.IsHub() }); av != nil {
			return av, true
		}
		if origin != nil {
			for _, s := range best {
				if origin[s.ID] && !isVillageStop(s) {
					return s, true
				}
			}
			for _, s := range best {
				if origin[s.ID] {
					return s, true
				}
			}
		}
		for _, s := range best {
			if !isVillageStop(s) {
				return s, true
			}
		}
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
	if p.logger != nil {
		p.logger.Debug("csa start", "from", fromStop, "to", toStop, "depart", depart, "connections", len(net.Connections))
	}
	maxTransfers := params.MaxTransfers
	if maxTransfers < 0 {
		maxTransfers = -1
	}

	allowed := map[model.Mode]bool{}
	for _, m := range params.AllowedModes {
		allowed[m] = true
	}

	conns := net.Connections
	if len(conns) == 0 || conns[0].Departure.After(conns[len(conns)-1].Departure) {
		tmp := make([]model.Connection, len(net.Connections))
		copy(tmp, net.Connections)
		sort.Slice(tmp, func(i, j int) bool {
			if tmp[i].Departure.Equal(tmp[j].Departure) {
				return tmp[i].Arrival.Before(tmp[j].Arrival)
			}
			return tmp[i].Departure.Before(tmp[j].Departure)
		})
		conns = tmp
	}

	transfersFrom := net.TransfersByStop
	if len(transfersFrom) == 0 && len(net.Transfers) > 0 {
		transfersFrom = map[string][]model.Transfer{}
		for _, tr := range net.Transfers {
			transfersFrom[tr.FromStopID] = append(transfersFrom[tr.FromStopID], tr)
		}
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
		if isImplausibleConnection(c, net) {
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

func (p *Planner) planArrival(net *model.Network, fromStop, toStop string, arrival, latestArrivalAtStop time.Time, params model.SearchParams) ([]model.Leg, time.Time, error) {
	windowStart := arrival.Add(-24 * time.Hour)
	if windowStart.After(latestArrivalAtStop) {
		windowStart = latestArrivalAtStop
	}
	// Collect candidate departures from trips that depart from fromStop
	candidates := []time.Time{}
	for _, trip := range net.Trips {
		for _, st := range trip.StopTimes {
			if st.StopID != fromStop {
				continue
			}
			dep := time.Date(latestArrivalAtStop.Year(), latestArrivalAtStop.Month(), latestArrivalAtStop.Day(), 0, 0, 0, 0, time.UTC).Add(time.Duration(st.DepartureSec) * time.Second)
			for dep.After(latestArrivalAtStop) {
				dep = dep.Add(-24 * time.Hour)
			}
			for dep.Before(windowStart) {
				dep = dep.Add(24 * time.Hour)
			}
			if dep.After(windowStart) && !dep.After(latestArrivalAtStop) {
				candidates = append(candidates, dep)
			}
			break
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].After(candidates[j]) })
	// fallback to 30m grid if no candidates
	if len(candidates) == 0 {
		for d := latestArrivalAtStop; !d.Before(windowStart); d = d.Add(-30 * time.Minute) {
			candidates = append(candidates, d)
		}
	}
	bestLegs := []model.Leg{}
	var bestDep time.Time
	for _, dep := range candidates {
		var legs []model.Leg
		var err error
		if p.engine == "raptor" {
			legs, err = p.raptor(net, fromStop, toStop, dep, params)
		} else {
			legs, err = p.csa(net, fromStop, toStop, dep, params)
		}
		if err != nil {
			continue
		}
		if len(legs) == 0 {
			continue
		}
		arr := legs[len(legs)-1].Arrival
		if arr.After(latestArrivalAtStop) {
			continue
		}
		bestLegs = legs
		bestDep = dep
		break
	}
	if len(bestLegs) == 0 {
		return nil, time.Time{}, fmt.Errorf("planner: маршрут между %s и %s не найден (нет рейсов до %s)", fromStop, toStop, arrival.Format(time.RFC3339))
	}
	return bestLegs, bestDep, nil
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

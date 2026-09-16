package planner

import (
	"fmt"
	"sort"
	"time"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
)

// mcLabel — Pareto-label McRAPTOR: earliest known arrival at a stop with a
// given trip count. Criteria are (arrival, trips); parent chains the
// reconstruction path, trip remembers the vehicle the label sits in
// (same-trip continuation is not a new boarding).
type mcLabel struct {
	stop   string
	arr    time.Time
	trips  int
	trip   string
	pr     *prev
	parent *mcLabel
}

// dominates — L dominates o when both criteria are no worse and at least
// one is strictly better.
func (l *mcLabel) dominates(o *mcLabel) bool {
	if l.arr.After(o.arr) || l.trips > o.trips {
		return false
	}
	return l.arr.Before(o.arr) || l.trips < o.trips
}

// mergeLabel — inserts L into the stop bag, dropping labels it dominates.
// Reports whether the bag improved (caller marks the stop for next round).
// Equal labels (same arrival and trip count) collapse to one: without the
// tie-break equivalent paths multiply exponentially across rounds, and a
// zero-minute foot cycle would re-improve forever instead of terminating.
func mergeLabel(bags map[string][]*mcLabel, L *mcLabel) bool {
	cur := bags[L.stop]
	for _, o := range cur {
		if o.dominates(L) {
			return false
		}
		if o.arr.Equal(L.arr) && o.trips == L.trips {
			return false
		}
	}
	kept := make([]*mcLabel, 0, len(cur)+1)
	for _, o := range cur {
		if !L.dominates(o) {
			kept = append(kept, o)
		}
	}
	bags[L.stop] = append(kept, L)
	return true
}

// mcraptor — multi-criteria RAPTOR departure query. Unlike csa/raptor, which
// minimize arrival time and check the transfer limit afterwards, the trip
// count is a first-class criterion here: every stop holds a bag of
// non-dominated (arrival, trips) labels, and the target label is the
// earliest arrival within the transfer limit. Boarding/day-roll/buffer/speed
// semantics mirror raptor; no-route falls back to csa like raptor does.
func (p *Planner) mcraptor(net *model.Network, fromStop, toStop string, depart time.Time, params model.SearchParams) ([]model.Leg, error) {
	bags := p.mcraptorBags(net, fromStop, depart, params)
	return p.mcraptorLegsFromBags(net, bags, fromStop, toStop, depart, params)
}

// mcraptorLegsFromBags — best-leg selection over precomputed bags: shared by
// the single-best query and the departure fast path in planWithStops, so one
// McRAPTOR search serves both best and alternatives.
func (p *Planner) mcraptorLegsFromBags(net *model.Network, bags map[string][]*mcLabel, fromStop, toStop string, depart time.Time, params model.SearchParams) ([]model.Leg, error) {
	best := selectTargetLabel(bags[toStop], params.MaxTransfers)
	if best == nil {
		return p.csa(net, fromStop, toStop, depart, params)
	}
	legs := legsForLabel(p, net, best, fromStop)
	if mx := params.MaxTransfers; mx > 0 && len(legs)-1 > mx {
		return nil, fmt.Errorf("planner: маршрут требует %d пересадок, больше лимита %d", len(legs)-1, mx)
	}
	if params.MaxTransfers == 0 && len(legs)-1 > 0 {
		return nil, fmt.Errorf("planner: маршрут требует пересадок, а лимит — без пересадок")
	}
	return legs, nil
}

// selectTargetLabel — earliest arrival within the transfer limit
// (trips-1, floor 0); ties go to fewer trips.
func selectTargetLabel(bag []*mcLabel, maxTransfers int) *mcLabel {
	var best *mcLabel
	for _, l := range bag {
		tr := l.trips - 1
		if tr < 0 {
			tr = 0
		}
		if maxTransfers >= 0 && tr > maxTransfers {
			continue
		}
		if best == nil || l.arr.Before(best.arr) || (l.arr.Equal(best.arr) && l.trips < best.trips) {
			best = l
		}
	}
	return best
}

// legsForLabel — reconstruct transit legs along the label parent chain.
// The chain may revisit a stop (foot loops); the pred map keeps the
// target-side visit so reconstruction terminates at the origin instead of
// cycling between two visits of the same stop.
func legsForLabel(p *Planner, net *model.Network, best *mcLabel, fromStop string) []model.Leg {
	pred := map[string]*prev{}
	arr := map[string]time.Time{}
	for cur := best; cur != nil; {
		if _, seen := pred[cur.stop]; !seen {
			pred[cur.stop] = cur.pr
			arr[cur.stop] = cur.arr
		}
		if cur.stop == fromStop {
			break
		}
		cur = cur.parent
	}
	return buildLegs(net, arr, p.reconstruct(pred, fromStop, best.stop))
}

// mcraptorBags — search core: Pareto label bags per stop. Shared by the
// single-best query and the native alternatives front.
func (p *Planner) mcraptorBags(net *model.Network, fromStop string, depart time.Time, params model.SearchParams) map[string][]*mcLabel {
	maxTransfers := params.MaxTransfers
	rounds := maxTransfers + 1
	if maxTransfers < 0 {
		// Same guard as raptor: effectively unlimited, insurance only.
		const defaultMaxTransfers = 100
		rounds = defaultMaxTransfers + 1
	}
	allowed := map[model.Mode]bool{}
	for _, m := range params.AllowedModes {
		allowed[m] = true
	}
	routeTrips := map[string][]*model.Trip{}
	for _, trip := range net.Trips {
		if len(allowed) > 0 && !allowed[trip.Mode] {
			continue
		}
		routeTrips[trip.RouteID] = append(routeTrips[trip.RouteID], trip)
	}
	routeIDs := make([]string, 0, len(routeTrips))
	for id := range routeTrips {
		routeIDs = append(routeIDs, id)
	}
	sort.Strings(routeIDs)
	for _, trips := range routeTrips {
		sort.Slice(trips, func(i, j int) bool {
			if len(trips[i].StopTimes) == 0 || len(trips[j].StopTimes) == 0 {
				return trips[i].ID < trips[j].ID
			}
			return trips[i].StopTimes[0].DepartureSec < trips[j].StopTimes[0].DepartureSec
		})
	}
	// routesByStop — inverted index: only routes serving a marked stop are
	// scanned in a round, instead of every route for every label.
	routesByStop := map[string][]string{}
	seenRouteStop := map[string]bool{}
	for _, route := range routeIDs {
		for _, trip := range routeTrips[route] {
			for _, st := range trip.StopTimes {
				key := route + "\x00" + st.StopID
				if !seenRouteStop[key] {
					seenRouteStop[key] = true
					routesByStop[st.StopID] = append(routesByStop[st.StopID], route)
				}
			}
		}
	}
	for stop := range routesByStop {
		sort.Strings(routesByStop[stop])
	}
	// tripBoardPos — first boarding position per (trip, stop): O(1) board
	// lookup in the round loop instead of scanning StopTimes per label.
	tripBoardPos := map[string]map[string]int{}
	for _, route := range routeIDs {
		for _, trip := range routeTrips[route] {
			pos := tripBoardPos[trip.ID]
			if pos == nil {
				pos = map[string]int{}
				tripBoardPos[trip.ID] = pos
			}
			for i, st := range trip.StopTimes {
				if _, ok := pos[st.StopID]; !ok {
					pos[st.StopID] = i
				}
			}
		}
	}
	transfersFrom := net.TransfersByStop
	if len(transfersFrom) == 0 {
		transfersFrom = map[string][]model.Transfer{}
		for _, tr := range net.Transfers {
			transfersFrom[tr.FromStopID] = append(transfersFrom[tr.FromStopID], tr)
		}
	}
	dayBase := time.Date(depart.Year(), depart.Month(), depart.Day(), 0, 0, 0, 0, time.UTC)

	earliest := map[string]time.Time{fromStop: depart}
	bags := map[string][]*mcLabel{}
	marked := map[string]bool{}
	improve := func(L *mcLabel) bool {
		if !mergeLabel(bags, L) {
			return false
		}
		marked[L.stop] = true
		if at, ok := earliest[L.stop]; !ok || L.arr.Before(at) {
			earliest[L.stop] = L.arr
		}
		return true
	}
	relaxFoot := func(L *mcLabel) {
		queue := []*mcLabel{L}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, tr := range transfersFrom[cur.stop] {
				if tr.ToStopID == cur.stop {
					continue
				}
				cand := cur.arr.Add(time.Duration(tr.Minutes) * time.Minute)
				if tr.MinTransferTime > 0 && tr.MinTransferTime != tr.Minutes {
					cand = cur.arr.Add(time.Duration(tr.MinTransferTime) * time.Minute)
				}
				nl := &mcLabel{stop: tr.ToStopID, arr: cand, trips: cur.trips, pr: &prev{footFrom: cur.stop}, parent: cur}
				if improve(nl) {
					queue = append(queue, nl)
				}
			}
		}
	}
	origin := &mcLabel{stop: fromStop, arr: depart}
	improve(origin)
	relaxFoot(origin)

	boardTrip := func(wl *mcLabel, trip *model.Trip, boardPos int) {
		tripsMade := wl.trips + 1
		if wl.trip != "" && wl.trip == trip.ID {
			tripsMade = wl.trips
		}
		for i := boardPos; i < len(trip.StopTimes)-1; i++ {
			fromST := trip.StopTimes[i]
			toST := trip.StopTimes[i+1]
			depT := dayBase.Add(time.Duration(fromST.DepartureSec) * time.Second)
			arrT := dayBase.Add(time.Duration(toST.ArrivalSec) * time.Second)
			if arrT.Before(depT) {
				arrT = arrT.Add(24 * time.Hour)
			}
			for depT.Before(earliest[fromST.StopID]) {
				depT = depT.Add(24 * time.Hour)
				arrT = arrT.Add(24 * time.Hour)
			}
			if depT.Before(wl.arr) {
				continue
			}
			if i == boardPos && wl.trip != "" && wl.trip != trip.ID {
				buf := p.minTransfer
				if trip.Mode == model.ModeFlight {
					buf = p.flightCheckIn
				}
				if buf > 0 && depT.Before(wl.arr.Add(time.Duration(buf)*time.Minute)) {
					continue
				}
			}
			if fromStopObj, ok1 := net.Stops[fromST.StopID]; ok1 {
				if toStopObj, ok2 := net.Stops[toST.StopID]; ok2 {
					if isImplausibleLeg(fromStopObj, toStopObj, depT, arrT, trip.Mode) {
						continue
					}
				}
			}
			nl := &mcLabel{
				stop: toST.StopID, arr: arrT, trips: tripsMade, trip: trip.ID,
				pr: &prev{conn: &model.Connection{
					TripID: trip.ID, ProviderID: trip.ProviderID, RouteID: trip.RouteID, Mode: trip.Mode,
					From: fromST.StopID, To: toST.StopID, Departure: depT, Arrival: arrT,
				}},
				parent: wl,
			}
			if improve(nl) {
				relaxFoot(nl)
			}
		}
	}

	for round := 1; round <= rounds; round++ {
		if len(marked) == 0 {
			break
		}
		byStop := map[string][]*mcLabel{}
		for s := range marked {
			byStop[s] = append(byStop[s], bags[s]...)
			delete(marked, s)
		}
		stops := make([]string, 0, len(byStop))
		for s := range byStop {
			stops = append(stops, s)
		}
		sort.Strings(stops)
		for _, stop := range stops {
			labels := byStop[stop]
			sort.Slice(labels, func(i, j int) bool {
				if labels[i].arr.Equal(labels[j].arr) {
					return labels[i].trips < labels[j].trips
				}
				return labels[i].arr.Before(labels[j].arr)
			})
			for _, route := range routesByStop[stop] {
				trips := routeTrips[route]
				for _, wl := range labels {
					// Every departure at or after the label arrival is
					// boardable: a later trip with a fuller stopping
					// pattern may reach stops the earliest trip skips.
					for _, trip := range trips {
						boardPos, ok := tripBoardPos[trip.ID][stop]
						if !ok || boardPos >= len(trip.StopTimes)-1 {
							continue
						}
						dt := dayBase.Add(time.Duration(trip.StopTimes[boardPos].DepartureSec) * time.Second)
						for dt.Before(depart) {
							dt = dt.Add(24 * time.Hour)
						}
						for dt.Before(wl.arr) {
							dt = dt.Add(24 * time.Hour)
						}
						_ = dt
						boardTrip(wl, trip, boardPos)
					}
				}
			}
		}
	}

	return bags
}

// nativeAlternatives — alternatives front from the SAME McRAPTOR run family:
// non-dominated target labels (same departure) instead of the N re-runs
// with shifted departures. Assembly mirrors paretoAlternatives (transit
// legs + access/egress-adjusted endpoints, preference sort, best excluded,
// cap 2) so downstream swap/pricing behave identically.
func (p *Planner) nativeAlternatives(net *model.Network, from, to model.Coords, params model.SearchParams, fromStop, toStop *model.Stop, best *model.Journey, transitDepart time.Time) []model.Journey {
	bags := p.mcraptorBags(net, fromStop.ID, transitDepart, params)
	return p.nativeAlternativesFromBags(net, bags, from, to, params, fromStop, toStop, best)
}

// nativeAlternativesFromBags — same front over precomputed bags: the
// departure fast path in planWithStops reuses the bags built for the best
// journey instead of running a second full search.
func (p *Planner) nativeAlternativesFromBags(net *model.Network, bags map[string][]*mcLabel, from, to model.Coords, params model.SearchParams, fromStop, toStop *model.Stop, best *model.Journey) []model.Journey {
	labels := append([]*mcLabel(nil), bags[toStop.ID]...)
	pref := params.Preference
	if pref == "" {
		pref = model.PreferenceTransfers
	}
	if pref == model.PreferenceTransfers {
		sort.Slice(labels, func(i, j int) bool {
			tri, trj := labels[i].trips, labels[j].trips
			if tri != trj {
				return tri < trj
			}
			return labels[i].arr.Before(labels[j].arr)
		})
	} else {
		sort.Slice(labels, func(i, j int) bool {
			if labels[i].arr.Equal(labels[j].arr) {
				return labels[i].trips < labels[j].trips
			}
			return labels[i].arr.Before(labels[j].arr)
		})
	}
	accessMin := geo.WalkTimeMinutes(geo.Haversine(from, fromStop.Coordinates()))
	egressMin := geo.WalkTimeMinutes(geo.Haversine(to, toStop.Coordinates()))
	var alts []model.Journey
	for _, l := range labels {
		legs := legsForLabel(p, net, l, fromStop.ID)
		if len(legs) == 0 {
			continue
		}
		j := model.Journey{From: from, To: to, Legs: legs, Transfers: len(legs) - 1}
		j.Departure = legs[0].Departure.Add(-time.Duration(accessMin) * time.Minute)
		j.Arrival = legs[len(legs)-1].Arrival.Add(time.Duration(egressMin) * time.Minute)
		if j.Arrival.Equal(best.Arrival) && j.Transfers == best.Transfers {
			continue
		}
		alts = append(alts, j)
		if len(alts) >= 2 {
			break
		}
	}
	return alts
}

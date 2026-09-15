package planner

import (
	"fmt"
	"sort"
	"time"

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
func mergeLabel(bags map[string][]*mcLabel, L *mcLabel) bool {
	cur := bags[L.stop]
	for _, o := range cur {
		if o.dominates(L) {
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
	for _, trips := range routeTrips {
		sort.Slice(trips, func(i, j int) bool {
			if len(trips[i].StopTimes) == 0 || len(trips[j].StopTimes) == 0 {
				return trips[i].ID < trips[j].ID
			}
			return trips[i].StopTimes[0].DepartureSec < trips[j].StopTimes[0].DepartureSec
		})
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

	for round := 1; round <= rounds; round++ {
		if len(marked) == 0 {
			break
		}
		type work struct {
			stop string
			l    *mcLabel
		}
		var ws []work
		for s := range marked {
			for _, l := range bags[s] {
				ws = append(ws, work{stop: s, l: l})
			}
			delete(marked, s)
		}
		for _, trips := range routeTrips {
			for _, w := range ws {
				bestTripIdx := -1
				bestBoardPos := -1
				var bestBoardTime time.Time
				for ti, trip := range trips {
					for pos, st := range trip.StopTimes {
						if st.StopID != w.stop {
							continue
						}
						dt := dayBase.Add(time.Duration(st.DepartureSec) * time.Second)
						for dt.Before(depart) {
							dt = dt.Add(24 * time.Hour)
						}
						for dt.Before(w.l.arr) {
							dt = dt.Add(24 * time.Hour)
						}
						if bestTripIdx == -1 || dt.Before(bestBoardTime) {
							bestTripIdx = ti
							bestBoardPos = pos
							bestBoardTime = dt
						}
						break
					}
				}
				if bestTripIdx == -1 {
					continue
				}
				trip := trips[bestTripIdx]
				tripsMade := w.l.trips + 1
				if w.l.trip != "" && w.l.trip == trip.ID {
					tripsMade = w.l.trips
				}
				for i := bestBoardPos; i < len(trip.StopTimes)-1; i++ {
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
					if depT.Before(w.l.arr) {
						continue
					}
					if i == bestBoardPos && w.l.trip != "" && w.l.trip != trip.ID {
						buf := p.minTransfer
						if trip.Mode == model.ModeFlight {
							buf = p.flightCheckIn
						}
						if buf > 0 && depT.Before(w.l.arr.Add(time.Duration(buf)*time.Minute)) {
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
						parent: w.l,
					}
					if improve(nl) {
						relaxFoot(nl)
					}
				}
			}
		}
	}

	var best *mcLabel
	for _, l := range bags[toStop] {
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
	if best == nil {
		return p.csa(net, fromStop, toStop, depart, params)
	}
	pred := map[string]*prev{}
	arr := map[string]time.Time{}
	for cur := best; cur != nil; {
		pred[cur.stop] = cur.pr
		arr[cur.stop] = cur.arr
		if cur.stop == fromStop {
			break
		}
		cur = cur.parent
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

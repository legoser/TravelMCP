package planner

import (
	"fmt"
	"sort"
	"time"

	"travelmcp/internal/model"
)

func (p *Planner) raptor(net *model.Network, fromStop, toStop string, depart time.Time, params model.SearchParams) ([]model.Leg, error) {
	maxTransfers := params.MaxTransfers
	if maxTransfers < 0 {
		maxTransfers = 100
	}
	allowed := map[model.Mode]bool{}
	for _, m := range params.AllowedModes {
		allowed[m] = true
	}
	// Build route -> trips sorted by departure
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

	arr := map[string]time.Time{fromStop: depart}
	pred := map[string]*prev{}
	marked := map[string]bool{fromStop: true}

	relax := func(stopID string, queue *[]string, inQueue map[string]bool) {
		q := []string{stopID}
		for len(q) > 0 {
			cur := q[0]
			q = q[1:]
			curT := arr[cur]
			for _, tr := range transfersFrom[cur] {
				cand := curT.Add(time.Duration(tr.Minutes) * time.Minute)
				if tr.MinTransferTime > 0 && tr.MinTransferTime != tr.Minutes {
					cand = curT.Add(time.Duration(tr.MinTransferTime) * time.Minute)
				}
				if at, ok := arr[tr.ToStopID]; !ok || cand.Before(at) {
					arr[tr.ToStopID] = cand
					pred[tr.ToStopID] = &prev{footFrom: cur}
					if !inQueue[tr.ToStopID] {
						*queue = append(*queue, tr.ToStopID)
						inQueue[tr.ToStopID] = true
					}
				}
			}
		}
	}

	// initial foot relaxation
	initQueue := []string{}
	inQueue := map[string]bool{}
	relax(fromStop, &initQueue, inQueue)

	// Prepare trip departure/arrival mapping per connection via StopTimes
	// For RAPTOR we scan trips directly
	for round := 0; round <= maxTransfers+1; round++ {
		newMarked := map[string]bool{}
		// For each route, find earliest trip that can be boarded from marked stops
		for routeID, trips := range routeTrips {
			_ = routeID
			// Find best boarding stop among marked
			bestTripIdx := -1
			bestBoardPos := -1
			bestBoardTime := time.Time{}
			for ti, trip := range trips {
				for pos, st := range trip.StopTimes {
					if !marked[st.StopID] {
						continue
					}
					// Need departure at this stop >= arrival at stop
					dt := time.Date(depart.Year(), depart.Month(), depart.Day(), 0, 0, 0, 0, time.UTC).Add(time.Duration(st.DepartureSec) * time.Second)
					// Handle overnight: if dt before depart, add day
					for dt.Before(depart) {
						dt = dt.Add(24 * time.Hour)
					}
					for dt.Before(arr[st.StopID]) {
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
			// Scan trip from boarding position onwards
			dayBase := time.Date(depart.Year(), depart.Month(), depart.Day(), 0, 0, 0, 0, time.UTC)
			for i := bestBoardPos; i < len(trip.StopTimes)-1; i++ {
				fromST := trip.StopTimes[i]
				toST := trip.StopTimes[i+1]
				dep := dayBase.Add(time.Duration(fromST.DepartureSec) * time.Second)
				arrTime := dayBase.Add(time.Duration(toST.ArrivalSec) * time.Second)
				if arrTime.Before(dep) {
					arrTime = arrTime.Add(24 * time.Hour)
				}
				// Adjust for day offset if needed
				for dep.Before(arr[fromST.StopID]) {
					dep = dep.Add(24 * time.Hour)
					arrTime = arrTime.Add(24 * time.Hour)
				}
				if at, ok := arr[toST.StopID]; ok && !arrTime.Before(at) {
					continue
				}
				// Check if we can board (dep >= arrival at from)
				if atFrom, ok := arr[fromST.StopID]; ok && dep.Before(atFrom) {
					continue
				}
				arr[toST.StopID] = arrTime
				pred[toST.StopID] = &prev{conn: &model.Connection{
					TripID: trip.ID, ProviderID: trip.ProviderID, RouteID: trip.RouteID, Mode: trip.Mode,
					From: fromST.StopID, To: toST.StopID, Departure: dep, Arrival: arrTime,
				}}
				newMarked[toST.StopID] = true
				// relax transfers from this stop
				q := []string{}
				relax(toST.StopID, &q, map[string]bool{})
				for _, s := range q {
					newMarked[s] = true
				}
			}
		}
		if len(newMarked) == 0 {
			break
		}
		if _, ok := arr[toStop]; ok {
			// continue to see if fewer transfers can improve, but for minimal time we can break
		}
		marked = newMarked
	}

	if pred[toStop] == nil {
		// fallback to CSA for robustness
		return p.csa(net, fromStop, toStop, depart, params)
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

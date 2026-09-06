package mintrans

import (
	"encoding/json"
	"fmt"

	"travelmcp/internal/model"
)

type Dataset = reestrDataset

func ParseDataset(raw []byte) (Dataset, error) {
	if err := ValidateDatasetContract(raw); err != nil {
		return Dataset{}, err
	}
	var ds reestrDataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return Dataset{}, fmt.Errorf("реестр: разбор: %w", err)
	}
	return ds, nil
}

func FlattenTrips(ds Dataset) []model.FlatTrip {
	stops := map[string]reestrStop{}
	for _, st := range ds.Stops {
		stops[st.ID] = st
	}
	carrier := map[string]reestrRoute{}
	for _, r := range ds.Routes {
		carrier[r.Reg] = r
	}
	endpoints := map[string][2]string{}
	for _, sc := range ds.Schedules {
		for _, dir := range []string{"forward", "backward"} {
			if sc.Direction != dir {
				continue
			}
			if len(sc.Stops) == 0 {
				continue
			}
			ep := endpoints[sc.Route]
			if dir == "forward" || (ep[0] == "" && ep[1] == "") {
				ep[0] = sc.Stops[0].Stop
				ep[1] = sc.Stops[len(sc.Stops)-1].Stop
				endpoints[sc.Route] = ep
			}
		}
	}
	var out []model.FlatTrip
	for _, sc := range ds.Schedules {
		period := pickPeriod(sc)
		r := carrier[sc.Route]
		ep := endpoints[sc.Route]
		base := model.FlatTrip{
			RouteReg:   sc.Route,
			Direction:  sc.Direction,
			ServiceID:  sc.ServiceID,
			Carrier:    r.Carrier,
			CarrierINN: r.CarrierINN,
			RouteFrom:  ep[0],
			RouteTo:    ep[1],
		}
		if period == "" {
			base.FrequencyOnly = true
			out = append(out, base)
			continue
		}
		base.Period = period
		runs := runsCount(sc, period)
		for run := 0; run < runs; run++ {
			ft := base
			ft.Run = run
			prevEff := -1
			for _, s := range sc.Stops {
				b := blockOf(s, period)
				if b == nil {
					ft.Untimed = append(ft.Untimed, s.Stop)
					continue
				}
				arrMin, hasArr := timeAt(b.Arr, run)
				depMin, hasDep := timeAt(b.Dep, run)
				if !hasArr && !hasDep {
					ft.Untimed = append(ft.Untimed, s.Stop)
					continue
				}
				if !hasArr {
					arrMin = depMin
				}
				if !hasDep {
					depMin = arrMin
				}
				arrOff := 0
				for arrMin+arrOff*1440 < prevEff {
					arrOff++
				}
				eff := arrMin + arrOff*1440
				depOff := arrOff
				for depMin+depOff*1440 < eff {
					depOff++
				}
				effDep := depMin + depOff*1440
				prevEff = effDep
				st := stops[s.Stop]
				arr, dep := eff, effDep
				ft.Stops = append(ft.Stops, model.FlatStop{
					StopID: s.Stop,
					Name:   st.Name,
					Region: s.Region,
					Lat:    st.Lat,
					Lon:    st.Lon,
					ArrMin: &arr,
					DepMin: &dep,
				})
			}
			out = append(out, ft)
		}
	}
	return out
}

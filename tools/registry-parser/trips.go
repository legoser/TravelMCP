package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseDataset(raw []byte) (reestrDataset, error) {
	if err := ValidateDatasetContract(raw); err != nil {
		return reestrDataset{}, err
	}
	var ds reestrDataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return reestrDataset{}, fmt.Errorf("реестр: разбор: %w", err)
	}
	return ds, nil
}

// FlattenTrips — конвертация реестра в flat-рейсы. op_reg стопа попадает
// в Codes с system источника (source), поэтому конвейер не знает, что
// код называется op_reg и откуда он пришёл.
func FlattenTrips(ds reestrDataset, source string) ([]FlatTrip, FlattenStats) {
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
	var out []FlatTrip
	var stats FlattenStats
	for _, sc := range ds.Schedules {
		stats.Schedules++
		period := pickPeriod(sc)
		r := carrier[sc.Route]
		ep := endpoints[sc.Route]
		base := FlatTrip{
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
			stats.FrequencyOnly++
			out = append(out, base)
			continue
		}
		base.Period = period
		runs := runsCount(sc, period)
		for run := 0; run < runs; run++ {
			stats.Runs++
			ft := base
			ft.Run = run
			weekdays := allWeek()
			parity := false
			prevEff := -1
			for _, s := range sc.Stops {
				b := blockOf(s, period)
				if b == nil {
					ft.Untimed = append(ft.Untimed, s.Stop)
					ft.Stops = append(ft.Stops, untimedFlatStop(s, stops, source))
					continue
				}
				bd, _ := ParseBlockDays(blockDaysOf(b))
				if bd.None {
					ft.Untimed = append(ft.Untimed, s.Stop)
					ft.Stops = append(ft.Stops, untimedFlatStop(s, stops, source))
					continue
				}
				if bd.Parity {
					parity = true
				}
				arrMin, arrDays, arrHasDays, hasArr := cellAt(b.Arr, run)
				depMin, depDays, depHasDays, hasDep := cellAt(b.Dep, run)
				if !hasArr && !hasDep {
					ft.Untimed = append(ft.Untimed, s.Stop)
					ft.Stops = append(ft.Stops, untimedFlatStop(s, stops, source))
					continue
				}
				if !hasArr {
					arrMin = depMin
				}
				if !hasDep {
					depMin = arrMin
				}
				stopDays := blockDaysSet(bd)
				if arrHasDays {
					stopDays = intersectDays(stopDays, arrDays)
				}
				if depHasDays {
					stopDays = intersectDays(stopDays, depDays)
				}
				weekdays = intersectDays(weekdays, stopDays)
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
				fs := FlatStop{
					StopID: s.Stop,
					Name:   st.Name,
					Region: s.Region,
					Lat:    st.Lat,
					Lon:    st.Lon,
					ArrMin: &arr,
					DepMin: &dep,
				}
				if st.OpReg != "" {
					fs.Codes = []AdaptedIdentifier{{System: source, CodeType: "op_reg", Code: st.OpReg}}
				}
				ft.Stops = append(ft.Stops, fs)
			}
			if len(ft.Untimed) == 0 {
				if len(weekdays) == 0 {
					stats.DroppedEmptyWeekdays++
					continue
				}
				if parity {
					stats.ParityTrips++
				}
				ft.Weekdays = normWeekdays(weekdays)
				if ft.Weekdays != nil {
					stats.RestrictedTrips++
				}
			}
			stats.Trips++
			out = append(out, ft)
		}
	}
	return out, stats
}

func blockOf(st reestrSchedStop, period string) *reestrBlock {
	if period == "winter" {
		return st.Winter
	}
	return st.Summer
}

// untimedFlatStop — стоп без времён сохраняет позицию в последовательности
// (fallback §5.4): attach сматчит терминал и интерполирует время по соседям,
// пометив is_fuzzy (пассажиру — «время уточнять у перевозчика»).
func untimedFlatStop(s reestrSchedStop, stops map[string]reestrStop, source string) FlatStop {
	st := stops[s.Stop]
	fs := FlatStop{
		StopID: s.Stop,
		Name:   st.Name,
		Region: s.Region,
		Lat:    st.Lat,
		Lon:    st.Lon,
		IsFuzzy: true,
	}
	if st.OpReg != "" {
		fs.Codes = []AdaptedIdentifier{{System: source, CodeType: "op_reg", Code: st.OpReg}}
	}
	return fs
}

func blockHasTimes(b *reestrBlock) bool {
	for _, t := range b.Dep {
		if _, ok := parseTimeMinutes(t); ok {
			return true
		}
	}
	for _, t := range b.Arr {
		if _, ok := parseTimeMinutes(t); ok {
			return true
		}
	}
	return false
}

func pickPeriod(sched reestrSched) string {
	for _, st := range sched.Stops {
		if st.Winter != nil && blockHasTimes(st.Winter) {
			return "winter"
		}
	}
	for _, st := range sched.Stops {
		if st.Summer != nil && blockHasTimes(st.Summer) {
			return "summer"
		}
	}
	return ""
}

func runsCount(sched reestrSched, period string) int {
	n := 1
	for _, st := range sched.Stops {
		b := blockOf(st, period)
		if b == nil {
			continue
		}
		if l := len(b.Dep); l > n {
			n = l
		}
		if l := len(b.Arr); l > n {
			n = l
		}
	}
	return n
}

func cellAt(list []string, run int) (mins int, days []int, hasDays bool, ok bool) {
	if run >= len(list) {
		return 0, nil, false, false
	}
	raw := strings.TrimSpace(list[run])
	if raw == "" || IsNoServiceCell(raw) {
		return 0, nil, false, false
	}
	mins, days, hasDays, err := ParseCellTime(raw)
	if err != nil {
		return 0, nil, false, false
	}
	return mins, days, hasDays, true
}

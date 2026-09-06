package sync

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"travelmcp/internal/adapters/mintrans"
	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
	"travelmcp/internal/verification"
)

type AttachTerminal struct {
	ID            int64
	Name          string
	Lat           *float64
	Lon           *float64
	Settlement    string
	Transport     string
	Source        string
	Codes         []model.AdaptedIdentifier
	GeomFinalized bool
}

type AttachInput struct {
	Trips          []model.FlatTrip
	Terminals      []AttachTerminal
	PrevCanon      map[string][]string
	TrustRouteNK   bool
	Source         string
	ChurnThreshold float64
	MaxSpeedKmh    float64
	ParamsFor      func(model.DensityClass) verification.Params
	ClassForRegion func(string) model.DensityClass
}

type MatchedStopTime struct {
	Seq        int    `json:"seq"`
	TerminalID int64  `json:"terminal_id"`
	StopID     string `json:"stop_id"`
	ArrivalS   int    `json:"arrival_s"`
	DepartureS int    `json:"departure_s"`
}

type PromotableTrip struct {
	RouteNK        string            `json:"route_nk"`
	TripNK         string            `json:"trip_nk"`
	RouteReg       string            `json:"route_reg"`
	Direction      string            `json:"direction"`
	ServiceID      int               `json:"service_id"`
	Run            int               `json:"run"`
	IsSyntheticKey bool              `json:"is_synthetic_key"`
	WinnerSource   string            `json:"winner_source"`
	WinnerPeriod   string            `json:"winner_period"`
	Carrier        string            `json:"carrier"`
	CarrierINN     string            `json:"carrier_inn"`
	StopTimes      []MatchedStopTime `json:"stop_times"`
	RouteSynthetic bool              `json:"route_synthetic"`
}

type StagedTrip struct {
	RouteNK        string            `json:"route_nk"`
	TripNK         string            `json:"trip_nk"`
	RouteReg       string            `json:"route_reg"`
	Direction      string            `json:"direction"`
	ServiceID      int               `json:"service_id"`
	Run            int               `json:"run"`
	State          string            `json:"state"`
	Reason         string            `json:"reason"`
	Matched        []MatchedStopTime `json:"matched"`
	Unmatched      []string          `json:"unmatched"`
	IsSyntheticKey bool              `json:"is_synthetic_key"`
	Carrier        string            `json:"carrier"`
	CarrierINN     string            `json:"carrier_inn"`
}

type DeadTrip struct {
	RouteNK string `json:"route_nk"`
	TripNK  string `json:"trip_nk"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
}

type TombstonedTrip struct {
	RouteNK string `json:"route_nk"`
	TripNK  string `json:"trip_nk"`
}

type AttachReport struct {
	In            int                      `json:"in"`
	Promoted      []PromotableTrip         `json:"promoted"`
	Staged        []StagedTrip             `json:"staged"`
	Reviews       []model.ReviewQueueEntry `json:"-"`
	Dead          []DeadTrip               `json:"dead"`
	Tombstoned    []TombstonedTrip         `json:"tombstoned"`
	Churn         float64                  `json:"churn"`
	Disappearance float64                  `json:"disappearance"`
	Alert         bool                     `json:"alert"`
}

func AttachTrips(ctx context.Context, in AttachInput) (AttachReport, error) {
	var rep AttachReport
	if in.ParamsFor == nil {
		return rep, fmt.Errorf("sync attach: нет скоринговых параметров (ParamsFor)")
	}
	source := in.Source
	if source == "" {
		source = "mintrans"
	}
	threshold := in.ChurnThreshold
	if threshold <= 0 {
		threshold = 0.2
	}
	maxSpeed := in.MaxSpeedKmh
	if maxSpeed <= 0 {
		maxSpeed = 200
	}
	classFor := in.ClassForRegion
	if classFor == nil {
		classFor = func(string) model.DensityClass { return model.DensityUrban }
	}
	rep.In = len(in.Trips)
	current := map[string]string{}
	for _, ft := range in.Trips {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		routeNK := ft.RouteReg
		routeSynthetic := false
		if !in.TrustRouteNK {
			routeNK = mintrans.SyntheticKeyForRoute(ft.RouteReg, ft.RouteFrom, ft.RouteTo, ft.CarrierINN)
			routeSynthetic = true
		}
		tripNK := routeNK + "|" + ft.Direction + ":" + strconv.Itoa(ft.ServiceID) + ":" + strconv.Itoa(ft.Run)
		current[tripNK] = routeNK
		if ft.FrequencyOnly || len(ft.Stops) < 2 {
			rep.Staged = append(rep.Staged, StagedTrip{
				RouteNK: routeNK, TripNK: tripNK, RouteReg: ft.RouteReg,
				Direction: ft.Direction, ServiceID: ft.ServiceID, Run: ft.Run,
				State:          "awaiting_times",
				Reason:         "нет точных времён: frequency-only или меньше двух timed-стопов",
				IsSyntheticKey: true, Carrier: ft.Carrier, CarrierINN: ft.CarrierINN,
			})
			continue
		}
		if len(ft.Untimed) > 0 {
			rep.Staged = append(rep.Staged, StagedTrip{
				RouteNK: routeNK, TripNK: tripNK, RouteReg: ft.RouteReg,
				Direction: ft.Direction, ServiceID: ft.ServiceID, Run: ft.Run,
				State:          "incomplete_trip",
				Reason:         "стопы без времён, интерполяция запрещена",
				Unmatched:      append([]string{}, ft.Untimed...),
				IsSyntheticKey: true, Carrier: ft.Carrier, CarrierINN: ft.CarrierINN,
			})
			rep.Reviews = append(rep.Reviews, tripReview(source, routeNK, tripNK, "incomplete_trip", 0))
			continue
		}
		matched, worstReason, worstScore, unmatched := matchStops(ft, in.Terminals, source, classFor, in.ParamsFor)
		if len(unmatched) > 0 {
			rep.Staged = append(rep.Staged, StagedTrip{
				RouteNK: routeNK, TripNK: tripNK, RouteReg: ft.RouteReg,
				Direction: ft.Direction, ServiceID: ft.ServiceID, Run: ft.Run,
				State:          "incomplete_trip",
				Reason:         worstReason,
				Matched:        matched,
				Unmatched:      unmatched,
				IsSyntheticKey: true, Carrier: ft.Carrier, CarrierINN: ft.CarrierINN,
			})
			rep.Reviews = append(rep.Reviews, tripReview(source, routeNK, tripNK, worstReason, worstScore))
			continue
		}
		collapsed := collapseConsecutive(matched)
		if bad := checkMonotonic(collapsed); bad != "" {
			rep.Dead = append(rep.Dead, DeadTrip{RouteNK: routeNK, TripNK: tripNK, Reason: "non-monotonic", Detail: bad})
			continue
		}
		if bad := checkSpeeds(collapsed, in.Terminals, maxSpeed); bad != nil {
			bad.RouteNK, bad.TripNK = routeNK, tripNK
			rep.Dead = append(rep.Dead, *bad)
			continue
		}
		if soft := softSpeeds(collapsed, in.Terminals, maxSpeed); soft != "" {
			rep.Staged = append(rep.Staged, StagedTrip{
				RouteNK: routeNK, TripNK: tripNK, RouteReg: ft.RouteReg,
				Direction: ft.Direction, ServiceID: ft.ServiceID, Run: ft.Run,
				State:          "needs_review",
				Reason:         soft,
				Matched:        collapsed,
				IsSyntheticKey: true, Carrier: ft.Carrier, CarrierINN: ft.CarrierINN,
			})
			rep.Reviews = append(rep.Reviews, tripReview(source, routeNK, tripNK, "low_confidence", 0))
			continue
		}
		rep.Promoted = append(rep.Promoted, PromotableTrip{
			RouteNK: routeNK, TripNK: tripNK, RouteReg: ft.RouteReg,
			Direction: ft.Direction, ServiceID: ft.ServiceID, Run: ft.Run,
			IsSyntheticKey: true, RouteSynthetic: routeSynthetic,
			WinnerSource: source + ":" + ft.Period, WinnerPeriod: ft.Period,
			Carrier: ft.Carrier, CarrierINN: ft.CarrierINN,
			StopTimes: collapsed,
		})
	}
	sort.Slice(rep.Promoted, func(i, j int) bool { return rep.Promoted[i].TripNK < rep.Promoted[j].TripNK })
	sort.Slice(rep.Staged, func(i, j int) bool { return rep.Staged[i].TripNK < rep.Staged[j].TripNK })
	sort.Slice(rep.Dead, func(i, j int) bool { return rep.Dead[i].TripNK < rep.Dead[j].TripNK })
	tombstone(&rep, in.PrevCanon, current, threshold)
	if rep.In != len(rep.Promoted)+len(rep.Staged)+len(rep.Dead) {
		return rep, fmt.Errorf("sync attach: несходимость строк: in=%d promoted=%d staged=%d dead=%d",
			rep.In, len(rep.Promoted), len(rep.Staged), len(rep.Dead))
	}
	if rep.Alert {
		return rep, fmt.Errorf("sync attach: churn-alert: churn=%.3f disappearance=%.3f выше порога %.3f",
			rep.Churn, rep.Disappearance, threshold)
	}
	return rep, nil
}

func tripReview(source, routeNK, tripNK, reason string, score float64) model.ReviewQueueEntry {
	if reason != "incomplete_trip" && reason != "low_confidence" && reason != "duplicate_ambiguous" {
		reason = "incomplete_trip"
	}
	return model.ReviewQueueEntry{
		EntityType:  "trip",
		EntityID:    syntheticReviewID("trip", reason, source, routeNK+"|"+tripNK),
		Reason:      reason,
		Score:       score,
		Fingerprint: source + ":" + routeNK + "|" + tripNK,
	}
}

func matchStops(ft model.FlatTrip, terms []AttachTerminal, source string, classFor func(string) model.DensityClass, paramsFor func(model.DensityClass) verification.Params) ([]MatchedStopTime, string, float64, []string) {
	cands := make([]verification.PairItem, 0, len(terms))
	for _, t := range terms {
		cands = append(cands, verification.PairItem{
			Name: t.Name, Lat: t.Lat, Lon: t.Lon,
			Transport: t.Transport, Settlement: t.Settlement,
			Codes: t.Codes, Source: t.Source,
		})
	}
	var matched []MatchedStopTime
	var unmatched []string
	worstReason := "incomplete_trip"
	worstScore := 0.0
	for seq, s := range ft.Stops {
		stop := verification.PairItem{
			Name: s.Name, Lat: s.Lat, Lon: s.Lon,
			Settlement: namesim.ExtractSettlement(s.Name),
			Source:     source,
		}
		class := classFor(s.Region)
		idx, d, score := verification.MatchStopToTerminal(stop, cands, class, paramsFor(class))
		if d != verification.DecisionVerified {
			unmatched = append(unmatched, s.StopID)
			reason := string(d)
			if reason == string(verification.DecisionRejected) {
				reason = "incomplete_trip"
			}
			if reasonRank(reason) > reasonRank(worstReason) {
				worstReason = reason
				worstScore = score.Value
			}
			continue
		}
		arr, dep := 0, 0
		if s.ArrMin != nil {
			arr = *s.ArrMin * 60
		}
		if s.DepMin != nil {
			dep = *s.DepMin * 60
		}
		matched = append(matched, MatchedStopTime{
			Seq: seq, TerminalID: terms[idx].ID, StopID: s.StopID,
			ArrivalS: arr, DepartureS: dep,
		})
	}
	for i := range matched {
		matched[i].Seq = i
	}
	return matched, worstReason, worstScore, unmatched
}

func reasonRank(r string) int {
	switch r {
	case "duplicate_ambiguous":
		return 3
	case "low_confidence":
		return 2
	default:
		return 1
	}
}

func collapseConsecutive(matched []MatchedStopTime) []MatchedStopTime {
	var out []MatchedStopTime
	for _, m := range matched {
		if n := len(out); n > 0 && out[n-1].TerminalID == m.TerminalID {
			out[n-1].DepartureS = m.DepartureS
			continue
		}
		out = append(out, m)
	}
	for i := range out {
		out[i].Seq = i
	}
	return out
}

func checkMonotonic(matched []MatchedStopTime) string {
	for i, m := range matched {
		if m.ArrivalS > m.DepartureS {
			return fmt.Sprintf("стоп %d (%s): прибытие после отправления", i, m.StopID)
		}
		if i > 0 && m.ArrivalS < matched[i-1].DepartureS {
			return fmt.Sprintf("стоп %d (%s): время убывает по seq", i, m.StopID)
		}
	}
	return ""
}

func terminalByID(terms []AttachTerminal, id int64) *AttachTerminal {
	for i := range terms {
		if terms[i].ID == id {
			return &terms[i]
		}
	}
	return nil
}

func legSpeedKmH(a, b *AttachTerminal, durMin int) (float64, bool) {
	if a == nil || b == nil || a.Lat == nil || a.Lon == nil || b.Lat == nil || b.Lon == nil {
		return 0, false
	}
	distKm := geo.Haversine(model.Coords{Lat: *a.Lat, Lon: *a.Lon}, model.Coords{Lat: *b.Lat, Lon: *b.Lon})
	if durMin <= 0 {
		if distKm < 0.05 {
			return 0, false
		}
		return 1e9, true
	}
	return distKm / (float64(durMin) / 60), true
}

func checkSpeeds(matched []MatchedStopTime, terms []AttachTerminal, maxSpeed float64) *DeadTrip {
	for i := 1; i < len(matched); i++ {
		a := terminalByID(terms, matched[i-1].TerminalID)
		b := terminalByID(terms, matched[i].TerminalID)
		durMin := (matched[i].ArrivalS - matched[i-1].DepartureS) / 60
		speed, ok := legSpeedKmH(a, b, durMin)
		if !ok {
			continue
		}
		if a.GeomFinalized && b.GeomFinalized && speed > maxSpeed {
			return &DeadTrip{
				Reason: "overspeed",
				Detail: fmt.Sprintf("перегон %d→%d: %.0f км/ч выше предела %.0f", i-1, i, speed, maxSpeed),
			}
		}
	}
	return nil
}

func softSpeeds(matched []MatchedStopTime, terms []AttachTerminal, maxSpeed float64) string {
	for i := 1; i < len(matched); i++ {
		a := terminalByID(terms, matched[i-1].TerminalID)
		b := terminalByID(terms, matched[i].TerminalID)
		durMin := (matched[i].ArrivalS - matched[i-1].DepartureS) / 60
		speed, ok := legSpeedKmH(a, b, durMin)
		if !ok {
			continue
		}
		if speed > maxSpeed && !(a.GeomFinalized && b.GeomFinalized) {
			return fmt.Sprintf("перегон %d→%d: %.0f км/ч на неподтверждённой геометрии", i-1, i, speed)
		}
	}
	return ""
}

func tombstone(rep *AttachReport, prev map[string][]string, current map[string]string, threshold float64) {
	oldTrips := 0
	prevSet := map[string]string{}
	for route, trips := range prev {
		for _, t := range trips {
			prevSet[t] = route
			oldTrips++
		}
	}
	added, removed := 0, 0
	for t := range current {
		if _, ok := prevSet[t]; !ok {
			added++
		}
	}
	for t, route := range prevSet {
		if _, ok := current[t]; !ok {
			removed++
			rep.Tombstoned = append(rep.Tombstoned, TombstonedTrip{RouteNK: route, TripNK: t})
		}
	}
	sort.Slice(rep.Tombstoned, func(i, j int) bool { return rep.Tombstoned[i].TripNK < rep.Tombstoned[j].TripNK })
	newTrips := len(current)
	denom := oldTrips
	if newTrips > denom {
		denom = newTrips
	}
	if denom == 0 {
		denom = 1
	}
	rep.Churn = float64(added+removed) / float64(denom)
	if oldTrips > 0 {
		rep.Disappearance = float64(removed) / float64(oldTrips)
	}
	if oldTrips == 0 {
		return
	}
	rep.Alert = rep.Churn > threshold || rep.Disappearance > threshold
}

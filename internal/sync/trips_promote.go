package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

type TripsStore interface {
	UpsertCarrier(ctx context.Context, c store.CarrierRow) (int64, error)
	UpsertService(ctx context.Context, s store.ServiceRow) error
	UpsertRoute(ctx context.Context, r store.RouteRow) (int64, error)
	UpsertRouteRegion(ctx context.Context, routeID int64, region string) error
	FindRouteID(ctx context.Context, source, routeCode string) (int64, bool)
	TombstoneRoute(ctx context.Context, source, routeCode string) error
	UpsertTrip(ctx context.Context, t store.TripRow) (int64, error)
	FindTrip(ctx context.Context, routeID int64, tripCode string) (store.TripRow, bool)
	TombstoneTrip(ctx context.Context, tripID int64) error
	DeleteStopTimes(ctx context.Context, tripID int64) error
	UpsertStop(ctx context.Context, s store.StopRow) (int64, error)
	EnsureStopForTerminal(ctx context.Context, terminalID int64, lat, lon float64, name string) (int64, error)
	SetTerminalTag(ctx context.Context, id int64, key, value string) error
	ListCanonTrips(ctx context.Context, source string) (map[string][]string, error)
	UpsertStopTime(ctx context.Context, st store.StopTimeRow) error
	UpsertTripSource(ctx context.Context, s store.TripSourceRow) error
	UpsertStagingTrip(ctx context.Context, s store.StagingTripRow) (int64, error)
	ListStagingTrips(ctx context.Context, region string, limit int) ([]store.StagingTripRow, error)
	DeleteStagingTrip(ctx context.Context, source, routeCode, tripCode string) error
	PublishOutbox(ctx context.Context, aggregate, aggregateID, event, payload string) error
	ListOutbox(ctx context.Context, limit int) ([]store.OutboxEvent, error)
	DeleteOutbox(ctx context.Context, id int64) error
	SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error
}

type PersistSummary struct {
	Routes     int `json:"routes"`
	Trips      int `json:"trips"`
	StopTimes  int `json:"stop_times"`
	Staged     int `json:"staged"`
	Tombstoned int `json:"tombstoned"`
	Reviews    int `json:"reviews"`
	Dead       int `json:"dead"`
}

func SplitTripNK(tripNK string) string {
	if i := strings.LastIndex(tripNK, "|"); i >= 0 {
		return tripNK[i+1:]
	}
	return tripNK
}

func DirectionIDOf(direction string) *int {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "forward":
		v := 0
		return &v
	case "backward":
		v := 1
		return &v
	default:
		return nil
	}
}

func baseSource(winner string) string {
	if i := strings.Index(winner, ":"); i >= 0 {
		return winner[:i]
	}
	return winner
}

func tripDurationS(stopTimes []MatchedStopTime) *int {
	if len(stopTimes) < 2 {
		return nil
	}
	d := stopTimes[len(stopTimes)-1].DepartureS - stopTimes[0].ArrivalS
	if d < 0 {
		return nil
	}
	return &d
}

func regionOfRouteReg(routeReg string) string {
	if len(routeReg) >= 2 {
		return routeReg[:2]
	}
	return ""
}

func PersistAttachReport(ctx context.Context, db store.Store, rep AttachReport, source string) (PersistSummary, error) {
	var sum PersistSummary
	ts, ok := db.(TripsStore)
	if !ok {
		return sum, fmt.Errorf("sync trips: стор не умеет персист трипов (нет staging/outbox)")
	}
	if source == "" {
		source = "mintrans"
	}
	sum.Dead = len(rep.Dead)
	for _, p := range rep.Promoted {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n, err := persistPromotedTrip(ctx, db, ts, p, source)
		if err != nil {
			return sum, err
		}
		sum.Routes += n.routes
		sum.Trips++
		sum.StopTimes += n.stopTimes
	}
	for _, s := range rep.Staged {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		if err := persistStagedTrip(ctx, ts, s, source); err != nil {
			return sum, err
		}
		sum.Staged++
	}
	for _, r := range rep.Reviews {
		if err := ts.SaveReviewQueue(ctx, r); err != nil {
			return sum, err
		}
		sum.Reviews++
	}
	for _, t := range rep.Tombstoned {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		routeID, found := ts.FindRouteID(ctx, source, t.RouteNK)
		if !found {
			continue
		}
		trip, found := ts.FindTrip(ctx, routeID, SplitTripNK(t.TripNK))
		if !found {
			continue
		}
		if err := ts.TombstoneTrip(ctx, trip.ID); err != nil {
			return sum, err
		}
		sum.Tombstoned++
	}
	if want := len(rep.Promoted) + len(rep.Staged) + len(rep.Dead); rep.In != want {
		return sum, fmt.Errorf("sync trips: несходимость строк при персисте: in=%d promoted=%d staged=%d dead=%d",
			rep.In, len(rep.Promoted), len(rep.Staged), len(rep.Dead))
	}
	slog.Info("sync trips: отчёт записан", "routes", sum.Routes, "trips", sum.Trips,
		"stop_times", sum.StopTimes, "staged", sum.Staged, "tombstoned", sum.Tombstoned)
	return sum, nil
}

type promotedCounts struct {
	routes    int
	stopTimes int
}

func persistPromotedTrip(ctx context.Context, db store.Store, ts TripsStore, p PromotableTrip, source string) (promotedCounts, error) {
	var out promotedCounts
	err := db.WithTx(ctx, func(tx store.Store) error {
		tts, ok := tx.(TripsStore)
		if !ok {
			return fmt.Errorf("sync trips: транзакция не умеет персист трипов")
		}
		carrierID := int64(0)
		if p.CarrierINN != "" || p.Carrier != "" {
			id, err := tts.UpsertCarrier(ctx, store.CarrierRow{ProviderID: source, Name: p.Carrier, Code: p.CarrierINN, INN: p.CarrierINN})
			if err != nil {
				return err
			}
			carrierID = id
		}
		routeID, err := tts.UpsertRoute(ctx, store.RouteRow{
			ProviderID: source, CarrierID: carrierID,
			ExternalRouteCode: p.RouteNK, ShortName: p.RouteReg, LongName: p.RouteReg, Mode: "bus",
		})
		if err != nil {
			return err
		}
		out.routes = 1
		if reg := regionOfRouteReg(p.RouteReg); reg != "" {
			if err := tts.UpsertRouteRegion(ctx, routeID, reg); err != nil {
				return err
			}
		}
		if p.ServiceID != 0 {
			if err := tts.UpsertService(ctx, store.ServiceRow{ID: p.ServiceID, ProviderID: source}); err != nil {
				return err
			}
		}
		tripCode := SplitTripNK(p.TripNK)
		tripID, err := tts.UpsertTrip(ctx, store.TripRow{
			RouteID: routeID, ProviderID: source, ExternalTripCode: tripCode,
			Direction: p.Direction, DirectionID: DirectionIDOf(p.Direction),
			ServiceID: p.ServiceID, Period: p.WinnerPeriod,
			DurationS: tripDurationS(p.StopTimes),
		})
		if err != nil {
			return err
		}
		if err := tts.DeleteStopTimes(ctx, tripID); err != nil {
			return err
		}
		for _, m := range p.StopTimes {
			stopID, err := tts.EnsureStopForTerminal(ctx, m.TerminalID, 0, 0, "")
			if err != nil {
				return err
			}
			if err := tts.UpsertStopTime(ctx, store.StopTimeRow{
				TripID: tripID, StopID: stopID, Seq: m.Seq,
				Arrival: m.ArrivalS, Departure: m.DepartureS,
			}); err != nil {
				return err
			}
			out.stopTimes++
		}
		if err := tts.UpsertTripSource(ctx, store.TripSourceRow{
			TripID: tripID, Source: baseSource(p.WinnerSource), DurationS: tripDurationS(p.StopTimes),
		}); err != nil {
			return err
		}
		for _, m := range p.StopTimes {
			if err := tts.SetTerminalTag(ctx, m.TerminalID, "intercity", "1"); err != nil {
				return err
			}
		}
		if err := tts.DeleteStagingTrip(ctx, source, p.RouteNK, tripCode); err != nil {
			return err
		}
		return nil
	})
	return out, err
}

func persistStagedTrip(ctx context.Context, ts TripsStore, s StagedTrip, source string) error {
	matched, _ := json.Marshal(s.Matched)
	if string(matched) == "null" {
		matched = []byte("[]")
	}
	unmatched, _ := json.Marshal(s.Unmatched)
	if string(unmatched) == "null" {
		unmatched = []byte("[]")
	}
	raw, _ := json.Marshal(map[string]any{
		"route_reg": s.RouteReg, "direction": s.Direction,
		"service_id": s.ServiceID, "run": s.Run,
		"carrier": s.Carrier, "carrier_inn": s.CarrierINN,
		"reason": s.Reason,
	})
	region := regionOfRouteReg(s.RouteReg)
	if region == "" {
		region = regionOfRouteReg(s.RouteNK)
	}
	_, err := ts.UpsertStagingTrip(ctx, store.StagingTripRow{
		Source: source, ExternalRouteCode: s.RouteNK, ExternalTripCode: SplitTripNK(s.TripNK),
		IsSyntheticKey: s.IsSyntheticKey, RouteRaw: string(raw),
		Region: region, TransportType: "bus", State: s.State,
		MatchedStopTimes: string(matched), UnmatchedStops: string(unmatched),
	})
	return err
}

func PublishTerminalCreated(ctx context.Context, ts TripsStore, terminalID int64, region string) error {
	payload, _ := json.Marshal(map[string]any{"terminal_id": terminalID, "region": region})
	return ts.PublishOutbox(ctx, "terminal", fmt.Sprint(terminalID), "terminal.created", string(payload))
}

func ConsumeTerminalCreated(ctx context.Context, ts TripsStore, handle func(region string) error) (int, error) {
	events, err := ts.ListOutbox(ctx, 100)
	if err != nil {
		return 0, err
	}
	regions := map[string]bool{}
	var ids []int64
	for _, e := range events {
		if e.Aggregate != "terminal" || e.Event != "terminal.created" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(e.Payload), &payload); err == nil {
			if r, _ := payload["region"].(string); r != "" {
				regions[r] = true
			}
		}
		ids = append(ids, e.ID)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var sorted []string
	for r := range regions {
		sorted = append(sorted, r)
	}
	sort.Strings(sorted)
	for _, r := range sorted {
		if err := handle(r); err != nil {
			return 0, err
		}
	}
	for _, id := range ids {
		if err := ts.DeleteOutbox(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

var _ = time.Now

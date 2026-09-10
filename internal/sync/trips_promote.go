package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/adapters/mintrans"
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
	AttachTerminalIdentifier(ctx context.Context, terminalID int64, id model.AdaptedIdentifier) (conflictWith int64, err error)
	ListTerminalCodes(ctx context.Context, terminalID int64, system string) ([]model.AdaptedIdentifier, error)
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
	ResolveTripReviewsByFingerprint(ctx context.Context, fingerprint string) (int, error)
	SaveProvenance(ctx context.Context, p model.Provenance) error
}

type PersistSummary struct {
	Routes          int `json:"routes"`
	Trips           int `json:"trips"`
	StopTimes       int `json:"stop_times"`
	Staged          int `json:"staged"`
	SkeletonGap     int `json:"skeleton_gap"`
	Tombstoned      int `json:"tombstoned"`
	Reviews         int `json:"reviews"`
	ReviewsResolved int `json:"reviews_resolved"`
	Dead            int `json:"dead"`
	Restricted      int `json:"restricted"`
	Codes           int `json:"codes_attached"`
	CodeClash       int `json:"code_conflicts"`
	MidGaps         int `json:"mid_gaps"`
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

func PersistAttachReport(ctx context.Context, db store.Store, rep AttachReport, source string, logger *slog.Logger) (PersistSummary, error) {
	var sum PersistSummary
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "persist_attach")
	if rep.Alert {
		return sum, fmt.Errorf("sync trips: churn-alert: накопление идентификаторов заморожено, персист запрещён")
	}
	ts, ok := db.(TripsStore)
	if !ok {
		return sum, fmt.Errorf("sync trips: стор не умеет персист трипов (нет staging/outbox)")
	}
	if source == "" {
		source = "mintrans"
	}
	sum.Dead = len(rep.Dead)
	sum.MidGaps = rep.MidGaps
	runCodes := map[int64]map[string]bool{}
	for _, p := range rep.Promoted {
		for _, m := range p.StopTimes {
			for _, code := range m.Codes {
				if runCodes[m.TerminalID] == nil {
					runCodes[m.TerminalID] = map[string]bool{}
				}
				runCodes[m.TerminalID][code.System+"\x00"+code.CodeType+"\x00"+code.Code] = true
			}
		}
	}
	for _, p := range rep.Promoted {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n, err := persistPromotedTrip(ctx, db, ts, p, source, runCodes, logger)
		if err != nil {
			return sum, err
		}
		sum.Routes += n.routes
		sum.Trips++
		sum.StopTimes += n.stopTimes
		if n.restricted {
			sum.Restricted++
		}
		sum.Codes += n.codesAttached
		sum.CodeClash += n.codeClash
		sum.ReviewsResolved += n.reviewsResolved
	}
	for _, s := range rep.Staged {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		if err := persistStagedTrip(ctx, ts, s, source); err != nil {
			return sum, err
		}
		sum.Staged++
		if s.State == "skeleton_gap" {
			sum.SkeletonGap++
		}
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
	logger.Info("persist attach report done", "routes", sum.Routes, "trips", sum.Trips,
		"stop_times", sum.StopTimes, "staged", sum.Staged, "tombstoned", sum.Tombstoned, "reviews_resolved", sum.ReviewsResolved)
	return sum, nil
}

type promotedCounts struct {
	routes          int
	stopTimes       int
	restricted      bool
	codesAttached   int
	codeClash       int
	reviewsResolved int
}

func persistPromotedTrip(ctx context.Context, db store.Store, ts TripsStore, p PromotableTrip, source string, runCodes map[int64]map[string]bool, logger *slog.Logger) (promotedCounts, error) {
	var out promotedCounts
	tripTag := logger.With("route_nk", p.RouteNK, "trip_nk", p.TripNK)
	tripTag.Debug("persisting trip")
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
		// provenance полноты §3.3: route несёт пару source/channel — вход
		// CI-gate при сборке GTFS (без записи zip не собирается)
		if err := tts.SaveProvenance(ctx, model.Provenance{EntityType: "route", EntityID: routeID, Source: source, Confidence: 1.0, ObservedAt: time.Now(), Channel: model.ChannelLocalFile}); err != nil {
			return err
		}
		if p.ServiceID != 0 {
			if err := tts.UpsertService(ctx, store.ServiceRow{ID: p.ServiceID, ProviderID: source}); err != nil {
				return err
			}
		}
		tripCode := SplitTripNK(p.TripNK)
		serviceDays := mintrans.FormatWeekdays(p.Weekdays)
		tripID, err := tts.UpsertTrip(ctx, store.TripRow{
			RouteID: routeID, ProviderID: source, ExternalTripCode: tripCode,
			Direction: p.Direction, DirectionID: DirectionIDOf(p.Direction),
			ServiceID: p.ServiceID, ServiceDays: serviceDays, Period: p.WinnerPeriod,
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
			var matchScore *float64
			if m.MatchScore > 0 {
				v := m.MatchScore
				matchScore = &v
			}
			if err := tts.UpsertStopTime(ctx, store.StopTimeRow{
				TripID: tripID, StopID: stopID, Seq: m.Seq,
				Arrival: m.ArrivalS, Departure: m.DepartureS,
				IsProvisional: m.IsProvisional,
				MatchScore:    matchScore,
				MatchMethod:   m.MatchMethod,
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
		// provenance полноты §3.3: trip несёт пару source/channel
		if err := tts.SaveProvenance(ctx, model.Provenance{EntityType: "trip", EntityID: tripID, Source: source, Confidence: 1.0, ObservedAt: time.Now(), Channel: model.ChannelLocalFile}); err != nil {
			return err
		}
		for _, m := range p.StopTimes {
			if err := tts.SetTerminalTag(ctx, m.TerminalID, "intercity", "1"); err != nil {
				return err
			}
			attached := map[string]bool{}
			for _, code := range m.Codes {
				clash, err := tts.AttachTerminalIdentifier(ctx, m.TerminalID, code)
				if err != nil {
					return err
				}
				if clash != 0 {
					out.codeClash++
					if err := tts.SaveReviewQueue(ctx, model.ReviewQueueEntry{
						EntityType:  "terminal",
						EntityID:    m.TerminalID,
						Reason:      "possible_merge",
						Fingerprint: code.System + ":" + code.CodeType + ":" + code.Code,
					}); err != nil {
						return err
					}
					continue
				}
				out.codesAttached++
				attached[code.System+"\x00"+code.CodeType+"\x00"+code.Code] = true
			}
			if len(m.Codes) > 0 {
				existing, err := tts.ListTerminalCodes(ctx, m.TerminalID, m.Codes[0].System)
				if err != nil {
					return err
				}
				for _, ex := range existing {
					key := ex.System + "\x00" + ex.CodeType + "\x00" + ex.Code
					if attached[key] || runCodes[m.TerminalID][key] {
						continue
					}
					out.codeClash++
					if err := tts.SaveReviewQueue(ctx, model.ReviewQueueEntry{
						EntityType:  "terminal",
						EntityID:    m.TerminalID,
						Reason:      "possible_merge",
						Fingerprint: ex.System + ":" + ex.CodeType + ":" + ex.Code,
					}); err != nil {
						return err
					}
				}
			}
		}
		if err := tts.DeleteStagingTrip(ctx, source, p.RouteNK, tripCode); err != nil {
			return err
		}
		// Автозакрытие review при промоушене трипа: причина low_confidence/
		// incomplete_trip снята самим фактом промоушена (тот же NK). Без этого
		// решённые записи висят в очереди открытыми (§5.3 гигиена review).
		if n, err := tts.ResolveTripReviewsByFingerprint(ctx, source+":"+p.RouteNK+"|"+p.TripNK); err != nil {
			return err
		} else if n > 0 {
			out.reviewsResolved += n
		}
		out.restricted = serviceDays != ""
		return nil
	})
	if err != nil {
		tripTag.Error("persist trip failed", "error", err)
		return out, fmt.Errorf("sync trips: persist trip %s|%s failed: %w", p.RouteNK, p.TripNK, err)
	}
	tripTag.Debug("trip persisted", "stop_times", out.stopTimes, "codes", out.codesAttached, "code_clashes", out.codeClash)
	return out, nil
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
		"weekdays": s.Weekdays, "reason": s.Reason,
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

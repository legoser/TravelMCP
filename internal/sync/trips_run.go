package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	store "travelmcp/internal/store"
	"travelmcp/internal/support/namesim"
	"travelmcp/internal/verification"
)

type TripsRunnerStore interface {
	TripsStore
	LegacyMatchStore
	CreateSyncRun(ctx context.Context, r store.SyncRunRow) (int64, error)
	FinishSyncRun(ctx context.Context, id int64, state, summary string) error
}

type TripsRunConfig struct {
	Source         string
	TrustRouteNK   bool
	ChurnThreshold float64
	MaxSpeedKmh    float64
	CoverageGate   float64
	SoftScore      float64
	Margin         float64
	WaitForGate    time.Duration
	PollInterval   time.Duration
	Force          bool
	DryRun         bool
	Regions        []string
	Flatten        model.FlattenStats
	PlanID         string
	InputSHA       string
	Tag            string
	ParamsFor      func(model.DensityClass) verification.Params
	ClassFor       func(string) model.DensityClass
	Logger         *slog.Logger
}

type RouteOps struct {
	In       int `json:"in"`
	Promoted int `json:"promoted"`
	Staged   int `json:"staged"`
	Dead     int `json:"dead"`
}

type TripsRunSummary struct {
	Flatten      model.FlattenStats  `json:"flatten"`
	Coverage     []RegionCoverage    `json:"coverage"`
	GatePass     bool                `json:"gate_pass"`
	Blocked      []string            `json:"blocked"`
	Forced       bool                `json:"forced"`
	Routes       int                 `json:"routes"`
	Skipped      []string            `json:"skipped_routes"`
	ByRoute      map[string]RouteOps `json:"by_route"`
	Waited       bool                `json:"waited"`
	Persist      PersistSummary      `json:"persist"`
	FullTripRate float64             `json:"full_trip_rate"`
}

func SummarizeByRoute(rep AttachReport) map[string]RouteOps {
	out := map[string]RouteOps{}
	for _, p := range rep.Promoted {
		o := out[p.RouteNK]
		o.In++
		o.Promoted++
		out[p.RouteNK] = o
	}
	for _, s := range rep.Staged {
		o := out[s.RouteNK]
		o.In++
		o.Staged++
		out[s.RouteNK] = o
	}
	for _, d := range rep.Dead {
		o := out[d.RouteNK]
		o.In++
		o.Dead++
		out[d.RouteNK] = o
	}
	return out
}

func DefaultStopTerminalParamsFor(cfg config.Verification) func(model.DensityClass) verification.Params {
	return func(class model.DensityClass) verification.Params {
		return verification.DefaultStopTerminalParams(cfg, class)
	}
}

func UrbanClassFor(region string) model.DensityClass { return model.DensityUrban }

func SkeletonRowToAttachTerminals(rows []store.SkeletonTerminalRow) []AttachTerminal {
	out := make([]AttachTerminal, 0, len(rows))
	for _, r := range rows {
		var lat, lon *float64
		if r.Lat != 0 || r.Lon != 0 {
			la, lo := r.Lat, r.Lon
			lat, lon = &la, &lo
		}
		source := "osm"
		for _, id := range r.Identifiers {
			if id.System == "osm" {
				source = "osm"
				break
			}
			if source == "osm" {
				source = id.System
			}
		}
		out = append(out, AttachTerminal{
			ID: r.ID, Name: r.NameRu, Lat: lat, Lon: lon,
			Settlement: r.Settlement, Transport: r.Transport, Source: source,
			Codes:         r.Identifiers,
			GeomFinalized: r.EnrichmentStatus == "enriched",
		})
	}
	return out
}

func RegistryStopsFromFlatTrips(trips []model.FlatTrip) []RegistryStop {
	seen := map[string]bool{}
	var out []RegistryStop
	for _, ft := range trips {
		for _, s := range ft.Stops {
			if seen[s.StopID] {
				continue
			}
			seen[s.StopID] = true
			out = append(out, RegistryStop{
				Name: s.Name, Region: s.Region,
				Settlement: namesim.ExtractSettlement(s.Name),
				Codes:      s.Codes,
				Lat:        s.Lat, Lon: s.Lon,
			})
		}
	}
	return out
}

func RouteRegions(trips []model.FlatTrip) map[string][]string {
	sets := map[string]map[string]bool{}
	for _, ft := range trips {
		if sets[ft.RouteReg] == nil {
			sets[ft.RouteReg] = map[string]bool{}
		}
		for _, s := range ft.Stops {
			if s.Region != "" {
				sets[ft.RouteReg][s.Region] = true
			}
		}
	}
	out := map[string][]string{}
	for route, set := range sets {
		for r := range set {
			out[route] = append(out[route], r)
		}
		sort.Strings(out[route])
	}
	return out
}

func BeginTripsRun(ctx context.Context, st TripsRunnerStore, planID, inputSHA, tag string) (int64, error) {
	return st.CreateSyncRun(ctx, store.SyncRunRow{PlanID: planID, Kind: "trips_attach", InputSHA: inputSHA, Tag: tag})
}

func FinishTripsRun(ctx context.Context, st TripsRunnerStore, runID int64, state string, sum TripsRunSummary) error {
	raw, err := json.Marshal(sum)
	if err != nil {
		return err
	}
	return st.FinishSyncRun(ctx, runID, state, string(raw))
}

func RunTripsSync(ctx context.Context, db store.Store, st TripsRunnerStore, trips []model.FlatTrip, cfg TripsRunConfig) (TripsRunSummary, error) {
	var sum TripsRunSummary
	sum.Flatten = cfg.Flatten
	if cfg.Source == "" {
		return sum, fmt.Errorf("sync trips: source обязателен (flat_trips.json meta.source)")
	}
	if cfg.ParamsFor == nil {
		return sum, fmt.Errorf("sync trips: нет скоринговых параметров (ParamsFor)")
	}
	classFor := cfg.ClassFor
	if classFor == nil {
		classFor = UrbanClassFor
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	allowed := map[string]bool{}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "trips_sync")
	for _, r := range cfg.Regions {
		allowed[r] = true
	}
	filtered := trips
	if len(allowed) > 0 {
		filtered = nil
		for _, ft := range trips {
			regs := map[string]bool{}
			for _, s := range ft.Stops {
				regs[s.Region] = true
			}
			keep := false
			for r := range regs {
				if allowed[r] {
					keep = true
					break
				}
			}
			if keep {
				filtered = append(filtered, ft)
			}
		}
	}
	deadline := time.Now().Add(cfg.WaitForGate)
	waited := false
	var cov []RegionCoverage
	var blocked []string
	var gatePass bool
	var terms []AttachTerminal
	for {
		rows, err := st.ListSkeletonTerminals(ctx)
		if err != nil {
			return sum, err
		}
		terms = SkeletonRowToAttachTerminals(rows)
		cov = MeasureAttachCoverage(RegistryStopsFromFlatTrips(filtered), terms, cfg.SoftScore, cfg.ParamsFor, classFor, cfg.Source)
		gatePass, blocked = GatePassRegions(cov, cfg.CoverageGate)
		sum.Coverage, sum.Blocked, sum.GatePass = cov, blocked, gatePass
		if gatePass || cfg.Force || time.Now().After(deadline) {
			break
		}
		waited = true
		logger.Warn("gate not passed, waiting for skeleton", "blocked", blocked, "gate", cfg.CoverageGate)
		select {
		case <-ctx.Done():
			return sum, ctx.Err()
		case <-time.After(cfg.PollInterval):
		}
	}
	sum.Waited = waited
	if !gatePass && !cfg.Force {
		logger.Warn("starvation alert, regions blocked by gate", "blocked", blocked, "gate", cfg.CoverageGate)
	}
	sum.Forced = cfg.Force
	if filled, missing := ResolveStopCoords(filtered, terms, cfg.Source); missing > 0 {
		logger.Info("stops resolved via skeleton", "filled", filled, "missing", missing)
	}
	routeRegs := RouteRegions(filtered)
	attachable := map[string]bool{}
	var routes []string
	for route, regs := range routeRegs {
		routes = append(routes, route)
		ok := true
		if !cfg.Force {
			for _, r := range regs {
				for _, b := range blocked {
					if r == b {
						ok = false
						break
					}
				}
			}
		}
		attachable[route] = ok
		if !ok {
			sum.Skipped = append(sum.Skipped, route)
		}
	}
	sort.Strings(routes)
	sort.Strings(sum.Skipped)
	var in []model.FlatTrip
	for _, ft := range filtered {
		if attachable[ft.RouteReg] {
			in = append(in, ft)
		}
	}
	sum.Routes = len(routes) - len(sum.Skipped)
	prev, err := st.ListCanonTrips(ctx, cfg.Source)
	if err != nil {
		return sum, err
	}
	rep, err := AttachTrips(ctx, AttachInput{
		Trips: in, Terminals: terms,
		PrevCanon: prev, TrustRouteNK: cfg.TrustRouteNK, Source: cfg.Source,
		ChurnThreshold: cfg.ChurnThreshold, MaxSpeedKmh: cfg.MaxSpeedKmh,
		ParamsFor: cfg.ParamsFor, ClassForRegion: classFor, Logger: logger,
	})
	if err != nil {
		return sum, err
	}
	sum.ByRoute = SummarizeByRoute(rep)
	if rep.In > 0 {
		sum.FullTripRate = float64(len(rep.Promoted)) / float64(rep.In)
	} else {
		sum.FullTripRate = 1
	}
	if cfg.DryRun {
		return sum, nil
	}
	persist, err := PersistAttachReport(ctx, db, rep, cfg.Source, logger)
	if err != nil {
		return sum, err
	}
	sum.Persist = persist
	logger.Info("trips sync run complete", "routes", sum.Routes, "skipped", sum.Skipped,
		"promoted", len(rep.Promoted), "staged", len(rep.Staged), "dead", len(rep.Dead),
		"blocked", blocked, "full_rate", sum.FullTripRate)
	return sum, nil
}

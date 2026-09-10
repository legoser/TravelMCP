package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/logger"
	"travelmcp/internal/model"
	"travelmcp/internal/store"
	_ "travelmcp/internal/store/memory"
	_ "travelmcp/internal/store/postgres"
	"travelmcp/internal/sync"
)

// appVersion → sync.LogicVersionID() (plan_id от семантической версии, план §4.3).

func main() {
	var configPath, tag, flatOverride, regionsOverride, waitOverride string
	var force, dryRun bool
	flag.StringVar(&configPath, "config", "configs/config.dev.yaml", "путь к YAML-конфигу")
	flag.StringVar(&tag, "tag", "pilot-trips", "тег прогона")
	flag.StringVar(&flatOverride, "flat", "", "переопределить sync.flat_trips_path (flat_trips.json)")
	flag.StringVar(&regionsOverride, "regions", "", "прикрепить только регионы через запятую (пусто — все)")
	flag.StringVar(&waitOverride, "wait", "", "переопределить sync.attach_wait (например 5m, пусто — не ждать)")
	flag.BoolVar(&force, "force", false, "эскалация: attach несмотря на заблокированный gate")
	flag.BoolVar(&dryRun, "dry-run", false, "coverage + gate + attach без записи в БД")
	flag.Parse()

	ctx := context.Background()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}
	factory := logger.NewFactory(cfg.Log)
	slog.SetDefault(factory.For("main"))
	sc := cfg.Sync
	if flatOverride != "" {
		sc.FlatTripsPath = flatOverride
	}
	if waitOverride != "" {
		sc.AttachWait = waitOverride
	}
	if sc.FlatTripsPath == "" {
		slog.Error("sync.flat_trips_path пуст: нечего прикреплять (сгенерируйте flat_trips.json коннектором tools/registry-parser)")
		os.Exit(1)
	}
	raw, err := os.ReadFile(sc.FlatTripsPath)
	if err != nil {
		slog.Error("flat_trips read failed", "error", err)
		os.Exit(1)
	}
	var payload model.FlatPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		slog.Error("flat_trips parse failed", "error", err)
		os.Exit(1)
	}
	if payload.Meta.Source == "" {
		slog.Error("flat_trips: meta.source пуст — источник обязан быть задан коннектором")
		os.Exit(1)
	}
	trips := payload.Trips
	fstats := payload.Stats
	sum := sha256.Sum256(raw)
	inputSHA := hex.EncodeToString(sum[:])
	planID := sync.ComputePlanID(sync.LogicVersionID(), syncConfigHash(sc, force), []string{inputSHA})
	slog.Info("plan", "plan_id", planID, "input_sha", inputSHA, "source", payload.Meta.Source,
		"snapshot", payload.Meta.Snapshot, "trips", len(trips),
		"restricted_weekdays", fstats.RestrictedTrips, "parity", fstats.ParityTrips,
		"dropped_empty_weekdays", fstats.DroppedEmptyWeekdays)

	st, err := store.New(ctx, cfg.Store.DSN)
	if err != nil {
		slog.Error("store open failed", "error", err)
		os.Exit(1)
	}
	if st == nil {
		slog.Error("store dsn empty")
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()
	rst, ok := st.(sync.TripsRunnerStore)
	if !ok {
		slog.Error("store lacks trips-sync methods")
		os.Exit(1)
	}

	wait, err := parseWait(sc.AttachWait)
	if err != nil {
		slog.Error("bad sync.attach_wait", "error", err)
		os.Exit(1)
	}
	rcfg := sync.TripsRunConfig{
		Source:         payload.Meta.Source,
		ChurnThreshold: sc.TripsChurnThreshold, MaxSpeedKmh: sc.TripsMaxSpeedKmh,
		CoverageGate: sc.CoverageGate, SoftScore: sc.CoverageSoftScore,
		Margin:      cfg.Verification.ScoreMargin,
		WaitForGate: wait, Force: force, DryRun: dryRun, Regions: splitRegions(regionsOverride),
		Flatten: fstats,
		PlanID:  planID, InputSHA: inputSHA, Tag: tag,
		ParamsFor: sync.DefaultStopTerminalParamsFor(cfg.Verification),
		ClassFor:  sync.UrbanClassFor,
	}

	if dryRun {
		rcfg.Logger = factory.For("trips_sync")
		sum, err := sync.RunTripsSync(ctx, st, rst, trips, rcfg)
		if err != nil {
			slog.Error("dry run failed", "error", err)
			os.Exit(1)
		}
		out, _ := json.MarshalIndent(sum, "", "  ")
		fmt.Println(string(out))
		return
	}

	runID, err := sync.BeginTripsRun(ctx, rst, planID, inputSHA, tag)
	if err != nil {
		slog.Error("begin run failed", "error", err)
		os.Exit(1)
	}
	rcfg.Logger = factory.For("trips_sync").With("run_id", runID)
	opsPath := filepath.Join(sc.LogDir, fmt.Sprintf("sync_%d.log.jsonl", runID))
	if err := os.MkdirAll(sc.LogDir, 0o755); err != nil {
		slog.Error("log dir failed", "error", err)
		os.Exit(1)
	}
	sumRes, err := sync.RunTripsSync(ctx, st, rst, trips, rcfg)
	if err != nil {
		_ = sync.FinishTripsRun(ctx, rst, runID, "dead", sumRes)
		slog.Error("trips run failed", "run_id", runID, "error", err)
		os.Exit(1)
	}
	if err := appendOps(opsPath, runID, sumRes); err != nil {
		slog.Error("ops log failed", "error", err)
		os.Exit(1)
	}
	if err := sync.FinishTripsRun(ctx, rst, runID, "done", sumRes); err != nil {
		slog.Error("finish run failed", "error", err)
		os.Exit(1)
	}
	slog.Info("trips run done", "run_id", runID, "routes", sumRes.Routes,
		"full_rate", sumRes.FullTripRate, "blocked", sumRes.Blocked)
}

func syncConfigHash(sc config.Sync, force bool) string {
	raw, _ := json.Marshal(struct {
		Gate   float64 `json:"gate"`
		Soft   float64 `json:"soft"`
		Churn  float64 `json:"churn"`
		MaxSpd float64 `json:"max_speed"`
		Force  bool    `json:"force"`
	}{sc.CoverageGate, sc.CoverageSoftScore, sc.TripsChurnThreshold, sc.TripsMaxSpeedKmh, force})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func parseWait(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	return time.ParseDuration(strings.TrimSpace(s))
}

func splitRegions(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type opsLine struct {
	RunID    int64   `json:"run_id"`
	Route    string  `json:"route"`
	In       int     `json:"in"`
	Promoted int     `json:"promoted"`
	Staged   int     `json:"staged"`
	Coverage float64 `json:"full_trip_rate"`
	State    string  `json:"state"`
}

func appendOps(path string, runID int64, sum sync.TripsRunSummary) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	routes := make([]string, 0, len(sum.ByRoute))
	for route := range sum.ByRoute {
		routes = append(routes, route)
	}
	for _, s := range sum.Skipped {
		routes = append(routes, "skipped:"+s)
	}
	sort.Strings(routes)
	for _, route := range routes {
		o := sum.ByRoute[strings.TrimPrefix(route, "skipped:")]
		if err := enc.Encode(opsLine{RunID: runID, Route: route, In: o.In,
			Promoted: o.Promoted, Staged: o.Staged, Coverage: sum.FullTripRate, State: "done"}); err != nil {
			return err
		}
	}
	return nil
}

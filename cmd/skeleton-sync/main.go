package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"travelmcp/internal/adapters/nominatim"
	"travelmcp/internal/adapters/overpass"
	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	"travelmcp/internal/store"
	_ "travelmcp/internal/store/memory"
	_ "travelmcp/internal/store/postgres"
	"travelmcp/internal/support/httpx"
	"travelmcp/internal/support/namesim"
	"travelmcp/internal/sync"
)

const appVersion = "skeleton-sync/1"

func main() {
	var configPath, tag, osmOverride, yandexOverride, reestrOverride, regionOverride string
	var resumeRun int64
	var dryRun, noReverse, noOverpass bool
	flag.StringVar(&configPath, "config", "configs/config.dev.yaml", "путь к YAML-конфигу")
	flag.StringVar(&tag, "tag", "pilot-kuzbass", "тег прогона")
	flag.StringVar(&osmOverride, "osm", "", "переопределить sync.osm_path")
	flag.StringVar(&yandexOverride, "yandex", "", "переопределить sync.yandex_dump_path")
	flag.StringVar(&reestrOverride, "reestr", "", "переопределить sync.reestr_path")
	flag.StringVar(&regionOverride, "region", "", "переопределить sync.skeleton_region")
	flag.Int64Var(&resumeRun, "run", 0, "продолжить незавершённый прогон с этим sync_runs.id")
	flag.BoolVar(&dryRun, "dry-run", false, "построить join и coverage без записи в БД")
	flag.BoolVar(&noReverse, "no-reverse", false, "не обогащать адреса через Nominatim reverse")
	flag.BoolVar(&noOverpass, "no-overpass", false, "не обогащать через Overpass StationsAround")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	ctx := context.Background()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}
	sc := cfg.Sync
	if osmOverride != "" {
		sc.OsmPath = osmOverride
	}
	if yandexOverride != "" {
		sc.YandexDumpPath = yandexOverride
	}
	if reestrOverride != "" {
		sc.ReestrPath = reestrOverride
	}
	if regionOverride != "" {
		sc.SkeletonRegion = regionOverride
	}

	// bbox опционален: пустой — без фильтра (весь экстракт), режем регионами
	var bboxFilter *bbox
	if strings.TrimSpace(sc.Bbox) != "" {
		b, err := parseBbox(sc.Bbox)
		if err != nil {
			slog.Error("bad sync.bbox", "error", err)
			os.Exit(1)
		}
		bboxFilter = &b
	}
	osm, err := skeleton.OSMSource{Path: sc.OsmPath}.Load()
	if err != nil {
		slog.Error("osm load failed", "error", err)
		os.Exit(1)
	}
	if bboxFilter != nil {
		osm = filterBbox(osm, *bboxFilter)
	}
	yan, err := skeleton.YandexDumpSource{Path: sc.YandexDumpPath}.Load()
	if err != nil {
		slog.Error("yandex load failed", "error", err)
		os.Exit(1)
	}
	regions := parseRegionList(sc.SkeletonRegion)
	if len(regions) > 0 {
		yan = filterRegions(yan, regions)
	}
	slog.Info("sources loaded", "osm", len(osm), "yandex", len(yan), "regions", regions)

	outcome := joinPaged(osm, yan)
	slog.Info("join done", "canon", len(outcome.Canon), "unverified", len(outcome.Unverified), "ambiguous", len(outcome.DuplicateAmbiguous))

	inputSHA, err := hashInputs(sc.OsmPath, sc.YandexDumpPath, sc.ReestrPath)
	if err != nil {
		slog.Error("hash inputs failed", "error", err)
		os.Exit(1)
	}
	planID := sync.ComputePlanID(appVersion, syncConfigHash(sc), []string{inputSHA})
	slog.Info("plan", "plan_id", planID, "input_sha", inputSHA)

	if dryRun {
		runDryCoverage(sc, outcome)
		return
	}

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

	runID := resumeRun
	if runID == 0 {
		runID, err = sync.BeginSkeletonRun(ctx, mustSkeletonStore(st), planID, inputSHA, tag)
		if err != nil {
			slog.Error("begin run failed", "error", err)
			os.Exit(1)
		}
	}
	opsPath := filepath.Join(sc.LogDir, fmt.Sprintf("sync_%d.log.jsonl", runID))
	if err := os.MkdirAll(sc.LogDir, 0o755); err != nil {
		slog.Error("log dir failed", "error", err)
		os.Exit(1)
	}

	if !noOverpass && sc.OverpassMax > 0 {
		enrichFromOverpass(ctx, st, *cfg, sc.OverpassMax, &outcome)
	}

	if !noReverse {
		enrichAddresses(ctx, st, &outcome, *cfg)
	}
	chunks := sync.ChunkSkeleton(outcome, sc.SkeletonChunkSize)
	slog.Info("chunks", "count", len(chunks))
	if err := stageRecords(ctx, st, runID, osm, yan); err != nil {
		slog.Error("stage failed", "error", err)
		os.Exit(1)
	}

	total := sync.RunSummary{Chunks: len(chunks)}
	recovered, err := readOpsSummaries(opsPath)
	if err != nil {
		slog.Error("read ops log failed", "error", err)
		os.Exit(1)
	}
	for _, ch := range chunks {
		if err := ctx.Err(); err != nil {
			slog.Error("cancelled", "error", err)
			os.Exit(1)
		}
		chunkID, err := mustSkeletonStore(st).EnsureSyncChunk(ctx, runID, "terminal", ch.Key)
		if err != nil {
			slog.Error("ensure chunk failed", "error", err)
			os.Exit(1)
		}
		if prev, ok := recovered[ch.Key]; ok {
			if fresh, _ := mustSkeletonStore(st).GetSyncChunk(ctx, runID, "terminal", ch.Key); fresh.State == "done" && sync.ChunkFresh(fresh.PlanIDDone, planID) {
				accumulate(&total, prev)
				slog.Info("chunk skipped (fresh)", "chunk", ch.Key)
				continue
			}
		}
		sum, err := sync.PromoteSkeletonChunk(ctx, st, runID, ch)
		if err != nil {
			_ = mustSkeletonStore(st).CompleteSyncChunk(ctx, chunkID, planID, "dead", err.Error())
			slog.Error("promote failed", "chunk", ch.Key, "error", err)
			os.Exit(1)
		}
		if err := mustSkeletonStore(st).CompleteSyncChunk(ctx, chunkID, planID, "done", ""); err != nil {
			slog.Error("complete chunk failed", "error", err)
			os.Exit(1)
		}
		if err := appendOps(opsPath, runID, sum); err != nil {
			slog.Error("ops log failed", "error", err)
			os.Exit(1)
		}
		accumulate(&total, chunkOps(sum))
	}
	total.In = len(outcome.Canon) + len(outcome.Unverified)
	total.Unmatched = total.Review
	if err := sync.FinishSkeletonRun(ctx, mustSkeletonStore(st), runID, "done", total); err != nil {
		slog.Error("finish run failed", "error", err)
		os.Exit(1)
	}
	slog.Info("skeleton run done", "run_id", runID, "written", total.Written, "review", total.Review)

	phase0ID, err := sync.BeginPhase0Run(ctx, mustSkeletonStore(st), planID, inputSHA, tag)
	if err != nil {
		slog.Error("begin phase0 failed", "error", err)
		os.Exit(1)
	}
	legacySum, err := sync.MatchLegacyTerminals(ctx, st, phase0ID, sc.LegacyThreshold, skeleton.DefaultJoinConfig())
	if err != nil {
		_ = mustSkeletonStore(st).FinishSyncRun(ctx, phase0ID, "dead", "{}")
		slog.Error("phase0 failed", "error", err)
		os.Exit(1)
	}
	raw, _ := json.Marshal(legacySum)
	if err := mustSkeletonStore(st).FinishSyncRun(ctx, phase0ID, "done", string(raw)); err != nil {
		slog.Error("finish phase0 failed", "error", err)
		os.Exit(1)
	}
	slog.Info("phase0 done", "run_id", phase0ID, "matched", legacySum.Matched, "updated", legacySum.Updated, "review", legacySum.Review)

	if sc.ReestrPath != "" {
		if err := runCoverage(ctx, st, sc, tag); err != nil {
			slog.Error("coverage failed", "error", err)
			os.Exit(1)
		}
	}
}

func enrichFromOverpass(ctx context.Context, st store.Store, cfg config.Config, maxPoints int, outcome *skeleton.JoinOutcome) {
	if len(outcome.Unverified) == 0 {
		return
	}
	limit := 500
	if err := st.SetQuotaLimit(ctx, "osm", limit); err != nil {
		slog.Warn("overpass quota limit failed", "error", err)
	}
	adapter := overpass.New(cfg, httpx.New(slog.Default(), "overpass"))
	cache := geocoder.NewMapGeoCacheStore(24 * time.Hour)
	quotaFunc := func(ctx context.Context, provider string, lim int) (bool, int, error) {
		return st.TryConsumeQuota(ctx, provider, lim)
	}
	provider := geocoder.NewCachedStationsProvider(adapter, cache, quotaFunc, limit)
	skeleton.OverpassEnrich(ctx, outcome, provider, maxPoints, slog.Default())
}

func enrichAddresses(ctx context.Context, st store.Store, outcome *skeleton.JoinOutcome, cfg config.Config) {
	limit := cfg.Geocode.MaxCalls
	if limit <= 0 {
		limit = 500
	}
	if err := st.SetQuotaLimit(ctx, "nominatim", limit); err != nil {
		slog.Warn("nominatim quota limit failed", "error", err)
	}
	rev := geocoder.Geocoder(nominatim.New(cfg, httpx.New(slog.Default(), "geocoder")))
	done, skipped := 0, 0
	for i := range outcome.Canon {
		r := &outcome.Canon[i].Record
		if r.Extra != nil && r.Extra["address"] != "" {
			continue
		}
		if !r.HasCoords() {
			continue
		}
		if err := ctx.Err(); err != nil {
			break
		}
		ok, _, err := st.TryConsumeQuota(ctx, "nominatim", limit)
		if err != nil || !ok {
			slog.Warn("nominatim quota exhausted, reverse stopped", "enriched", done, "error", err)
			break
		}
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		addr, err := rev.Reverse(call, *r.Lat, *r.Lon)
		cancel()
		if err != nil {
			slog.Debug("nominatim reverse failed", "name", r.NameRu, "error", err)
			skipped++
			continue
		}
		if r.Extra == nil {
			r.Extra = map[string]string{}
		}
		r.Extra["address"] = addr
		done++
	}
	slog.Info("nominatim reverse done", "enriched", done, "skipped", skipped)
}

func mustSkeletonStore(st store.Store) interface {
	sync.SkeletonStore
	EnsureSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (int64, error)
	GetSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (store.SyncChunkRow, bool)
	CompleteSyncChunk(ctx context.Context, id int64, planIDDone, state, lastError string) error
} {
	s, ok := st.(interface {
		sync.SkeletonStore
		EnsureSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (int64, error)
		GetSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (store.SyncChunkRow, bool)
		CompleteSyncChunk(ctx context.Context, id int64, planIDDone, state, lastError string) error
	})
	if !ok {
		slog.Error("store lacks skeleton-sync methods")
		os.Exit(1)
	}
	return s
}

type bbox struct{ minLat, minLon, maxLat, maxLon float64 }

func parseBbox(s string) (bbox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return bbox{}, fmt.Errorf("bbox: ожидалось minLat,minLon,maxLat,maxLon, получено %q", s)
	}
	var b bbox
	vals := []*float64{&b.minLat, &b.minLon, &b.maxLat, &b.maxLon}
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return bbox{}, fmt.Errorf("bbox: %w", err)
		}
		*vals[i] = v
	}
	return b, nil
}

func filterBbox(in []model.AdaptedRecord, b bbox) []model.AdaptedRecord {
	out := make([]model.AdaptedRecord, 0, len(in))
	for _, r := range in {
		if !r.HasCoords() {
			continue
		}
		if *r.Lat < b.minLat || *r.Lat > b.maxLat || *r.Lon < b.minLon || *r.Lon > b.maxLon {
			continue
		}
		out = append(out, r)
	}
	return out
}

// parseRegionList — «Р1, Р2» → [Р1, Р2]; пусто/«all»/* → nil (без фильтра).
func parseRegionList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "all") || s == "*" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func filterRegions(in []model.AdaptedRecord, regions []string) []model.AdaptedRecord {
	allow := make(map[string]bool, len(regions))
	for _, r := range regions {
		allow[r] = true
	}
	out := make([]model.AdaptedRecord, 0, len(in))
	for _, r := range in {
		if r.Extra != nil && allow[r.Extra["region"]] {
			out = append(out, r)
		}
	}
	return out
}

// joinPaged — Join через пагинированный JoinPager (эквивалентен Join,
// O(N×M)/cells вместо O(N×M)): страницы по 100 OSM-записей, гео-ячейки 0.5°.
func joinPaged(osm, yan []model.AdaptedRecord) skeleton.JoinOutcome {
	pager := skeleton.NewJoinPager(osm, yan, skeleton.DefaultJoinConfig(), 100)
	var out skeleton.JoinOutcome
	for {
		page, ok := pager.NextPage()
		if !ok {
			break
		}
		out.Canon = append(out.Canon, page.Canon...)
		out.DuplicateAmbiguous = append(out.DuplicateAmbiguous, page.DuplicateAmbiguous...)
	}
	out.Unverified = pager.FlushUnverified()
	return out
}

func filterRegion(in []model.AdaptedRecord, region string) []model.AdaptedRecord {
	out := make([]model.AdaptedRecord, 0, len(in))
	for _, r := range in {
		if r.Extra == nil || r.Extra["region"] != region {
			continue
		}
		out = append(out, r)
	}
	return out
}

func hashInputs(paths ...string) (string, error) {
	h := sha256.New()
	for _, p := range paths {
		if p == "" {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return "", err
		}
		_ = f.Close()
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func syncConfigHash(sc config.Sync) string {
	raw, _ := json.Marshal(struct {
		Region          string  `json:"region"`
		Bbox            string  `json:"bbox"`
		Chunk           int     `json:"chunk"`
		LegacyThreshold float64 `json:"legacy_threshold"`
	}{sc.SkeletonRegion, sc.Bbox, sc.SkeletonChunkSize, sc.LegacyThreshold})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type opsLine struct {
	RunID    int64  `json:"run_id"`
	ChunkKey string `json:"chunk_key"`
	In       int    `json:"in"`
	Written  int    `json:"written"`
	Review   int    `json:"review"`
	Enriched int    `json:"enriched"`
	State    string `json:"state"`
}

func chunkOps(s sync.ChunkSummary) opsLine {
	return opsLine{ChunkKey: s.Key, In: s.In, Written: s.Written, Review: s.Review, Enriched: s.Enriched, State: "done"}
}

func accumulate(total *sync.RunSummary, o opsLine) {
	total.Written += o.Written
	total.Review += o.Review
	total.Enriched += o.Enriched
}

func appendOps(path string, runID int64, s sync.ChunkSummary) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	o := chunkOps(s)
	o.RunID = runID
	return json.NewEncoder(f).Encode(o)
}

func readOpsSummaries(path string) (map[string]opsLine, error) {
	out := map[string]opsLine{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var o opsLine
		if err := json.Unmarshal([]byte(line), &o); err != nil {
			return nil, fmt.Errorf("ops log %s: %w", path, err)
		}
		if o.State == "done" {
			out[o.ChunkKey] = o
		}
	}
	return out, nil
}

type stager interface {
	StageSkeletonRecords(ctx context.Context, runID int64, records []model.AdaptedRecord) error
}

func stageRecords(ctx context.Context, st store.Store, runID int64, osm, yan []model.AdaptedRecord) error {
	s, ok := st.(stager)
	if !ok {
		slog.Info("store lacks staging, skip")
		return nil
	}
	if err := s.StageSkeletonRecords(ctx, runID, osm); err != nil {
		return err
	}
	return s.StageSkeletonRecords(ctx, runID, yan)
}

func runDryCoverage(sc config.Sync, outcome skeleton.JoinOutcome) {
	skels := make([]store.SkeletonTerminalRow, 0, len(outcome.Canon))
	for _, j := range outcome.Canon {
		lat, lon := 0.0, 0.0
		if j.Record.HasCoords() {
			lat, lon = *j.Record.Lat, *j.Record.Lon
		}
		skels = append(skels, store.SkeletonTerminalRow{NameRu: j.Record.NameRu, Lat: lat, Lon: lon, Settlement: j.Record.Extra["settlement"]})
	}
	stops, err := loadRegistryStops(sc.ReestrPath)
	if err != nil {
		slog.Error("reestr load failed", "error", err)
		os.Exit(1)
	}
	cov := sync.MeasureCoverage(stops, skels, sc.CoverageSoftScore, skeleton.DefaultJoinConfig())
	ok, blocked := skeleton.GatePass(sync.ToSkeletonCoverages(cov), sc.CoverageGate)
	raw, _ := json.MarshalIndent(struct {
		Coverage []sync.RegionCoverage `json:"coverage"`
		GatePass bool                  `json:"gate_pass"`
		Blocked  []string              `json:"blocked"`
	}{cov, ok, blocked}, "", "  ")
	fmt.Println(string(raw))
}

type reestrStop struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

func loadRegistryStops(path string) ([]sync.RegistryStop, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Stops []reestrStop `json:"stops"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := make([]sync.RegistryStop, 0, len(doc.Stops))
	for _, s := range doc.Stops {
		out = append(out, sync.RegistryStop{Name: s.Name, Region: s.Region, Settlement: namesim.ExtractSettlement(s.Name)})
	}
	return out, nil
}

func runCoverage(ctx context.Context, st store.Store, sc config.Sync, tag string) error {
	lister, ok := st.(sync.LegacyMatchStore)
	if !ok {
		return fmt.Errorf("store lacks skeleton reads")
	}
	skels, err := lister.ListSkeletonTerminals(ctx)
	if err != nil {
		return err
	}
	stops, err := loadRegistryStops(sc.ReestrPath)
	if err != nil {
		return err
	}
	cov := sync.MeasureCoverage(stops, skels, sc.CoverageSoftScore, skeleton.DefaultJoinConfig())
	ok, blocked := skeleton.GatePass(sync.ToSkeletonCoverages(cov), sc.CoverageGate)
	raw, _ := json.MarshalIndent(struct {
		Tag       string                `json:"tag"`
		Terminals int                   `json:"terminals"`
		Stops     int                   `json:"stops"`
		Coverage  []sync.RegionCoverage `json:"coverage"`
		GatePass  bool                  `json:"gate_pass"`
		Blocked   []string              `json:"blocked"`
	}{tag, len(skels), len(stops), cov, ok, blocked}, "", "  ")
	fmt.Println(string(raw))
	slog.Info("coverage measured", "gate_pass", ok, "blocked", blocked)
	return nil
}

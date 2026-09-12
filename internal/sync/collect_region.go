// Пакет sync: интерактивный сбор региона (ручной режим, план §5.4):
// skeleton — Яндекс-дамп → канон unverified-терминалов (single-source),
// trips — расписания/нитки (cache-first Rasp) → flat → attach.
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
)

// CollectSkeletonConfig — параметры job'а сбора скелета.
type CollectSkeletonConfig struct {
	Regions      []string
	Transports   map[string]bool
	StationTypes map[string]bool
	YandexPath   string
	Tag          string
	Offline      bool
}

// CollectRegionSummary — сводка прогона сбора (уходит в sync_runs.summary
// и на экран «Прогоны»).
type CollectRegionSummary struct {
	Kind        string            `json:"kind"`
	Source      string            `json:"source"`
	Regions     []string          `json:"regions"`
	In          int               `json:"in"`
	Written     int               `json:"written"`
	Review      int               `json:"review"`
	Enriched    int               `json:"enriched"`
	Chunks      int               `json:"chunks"`
	Offline     bool              `json:"offline"`
	RaspCache   int               `json:"rasp_cache"`
	RaspAPI     int               `json:"rasp_api"`
	RaspBlocked int               `json:"rasp_quota_blocked"`
	Dropped     int               `json:"dropped"`
	RaspStats   *RaspCollectStats `json:"rasp_stats,omitempty"`
}

type RaspCollectStats struct {
	Schedules   int `json:"schedules"`
	Threads     int `json:"threads"`
	ThreadsDead int `json:"threads_dead"`
	Restricted  int `json:"restricted_days"`
	NoTimes     int `json:"no_times"`
	Empty       int `json:"empty"`
	Trips       int `json:"trips"`
}

// skeletonChunkRunner — узкий интерфейс чанковой проводки (без тащения
// EnsureSyncChunk/GetSyncChunk в общий SyncStore).
type skeletonChunkRunner interface {
	SkeletonStore
	EnsureSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (int64, error)
	GetSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (store.SyncChunkRow, bool)
	CompleteSyncChunk(ctx context.Context, id int64, planIDDone, state, lastError string) error
	StageSkeletonRecords(ctx context.Context, runID int64, records []model.AdaptedRecord) error
}

// RunCollectSkeleton — сбор скелета региона из Яндекс-дампа: загрузка →
// фильтры → (без OSM-join: single-source ручной режим) → чанковый промоут
// unverified-терминалов. Все записи с координатами идут в канон score 0.4
// (IdentityOnly), без координат — в review.
func RunCollectSkeleton(ctx context.Context, st store.Store, sc config.Sync, cc CollectSkeletonConfig, logger *slog.Logger) (CollectRegionSummary, error) {
	sum := CollectRegionSummary{Kind: "collect_skeleton", Source: "yandex", Regions: cc.Regions, Offline: cc.Offline}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "collect_skeleton", "tag", cc.Tag)

	yan, err := skeleton.YandexDumpSource{Path: cc.YandexPath}.Load()
	if err != nil {
		return sum, err
	}
	yan = skeleton.FilterYandexRecords(yan, cc.Regions, cc.Transports, cc.StationTypes)
	sum.In = len(yan)
	logger.Info("yandex records filtered", "in", sum.In)

	outcome := skeleton.JoinOutcome{Unverified: yan}
	chunks := ChunkSkeleton(outcome, sc.SkeletonChunkSize)
	sum.Chunks = len(chunks)

	inputSHA, err := fileContentSHA(cc.YandexPath)
	if err != nil {
		return sum, err
	}
	planID := ComputePlanID(LogicVersionID(), collectConfigHash(cc), []string{inputSHA})
	sstore := skeletonStoreOf(st)
	if sstore == nil {
		return sum, fmt.Errorf("collect skeleton: стор без skeleton-методов")
	}
	runID, err := BeginSkeletonRun(ctx, sstore, planID, inputSHA, cc.Tag)
	if err != nil {
		return sum, err
	}
	sr, ok := st.(skeletonChunkRunner)
	if !ok {
		return sum, fmt.Errorf("collect skeleton: стор без чанковой проводки")
	}
	if err := sr.StageSkeletonRecords(ctx, runID, yan); err != nil {
		logger.Warn("staging skipped", "error", err)
	}
	for _, ch := range chunks {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		chunkID, err := sr.EnsureSyncChunk(ctx, runID, "terminal", ch.Key)
		if err != nil {
			return sum, err
		}
		csum, err := PromoteSkeletonChunk(ctx, st, runID, ch)
		if err != nil {
			_ = sr.CompleteSyncChunk(ctx, chunkID, planID, "dead", err.Error())
			return sum, err
		}
		if err := sr.CompleteSyncChunk(ctx, chunkID, planID, "done", ""); err != nil {
			return sum, err
		}
		sum.Written += csum.Written
		sum.Review += csum.Review
		sum.Enriched += csum.Enriched
	}
	sum.Dropped = sum.In - sum.Written - sum.Review
	raw, _ := json.Marshal(sum)
	if err := skeletonStoreOf(st).FinishSyncRun(ctx, runID, "done", string(raw)); err != nil {
		return sum, err
	}
	logger.Info("collect skeleton done", "run_id", runID, "written", sum.Written, "review", sum.Review)
	return sum, nil
}

func finishRun(ctx context.Context, st store.Store, runID int64) error {
	_ = st
	_ = runID
	return nil
}

func skeletonStoreOf(st store.Store) SkeletonStore {
	s, _ := st.(SkeletonStore)
	return s
}

// fileContentSHA — sha256 содержимого файла (пустой путь — нулевой хэш:
// детерминизм plan_id сохраняется).
func fileContentSHA(path string) (string, error) {
	h := sha256.New()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func collectConfigHash(cc CollectSkeletonConfig) string {
	raw, _ := json.Marshal(struct {
		Regions      []string        `json:"regions"`
		Transports   map[string]bool `json:"transports"`
		StationTypes map[string]bool `json:"station_types"`
	}{cc.Regions, cc.Transports, cc.StationTypes})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CollectTripsConfig — параметры job'а сбора рейсов.
type CollectTripsConfig struct {
	Region  string
	Date    string
	Tag     string
	Offline bool
	Force   bool
	Trips   []model.FlatTrip
	Flatten model.FlattenStats
	Source  string
	// TerminalScope — точечный прогон одного терминала: churn-гейт
	// ограничен затронутыми маршрутами.
	TerminalScope bool
}

// RunCollectTrips — сбор рейсов региона: готовые flat-рейсы (конвертация
// ниток Rasp сделана адаптером в caller'е) → полный attach-конвейер
// (coverage-gate, матчинг стопов, валидаторы, промоушен).
func RunCollectTrips(ctx context.Context, st store.Store, rst TripsRunnerStore, sc config.Sync, cfg config.Config, cc CollectTripsConfig, logger *slog.Logger) (TripsRunSummary, error) {
	var sum TripsRunSummary
	sum.Flatten = cc.Flatten
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "collect_trips", "tag", cc.Tag)
	source := cc.Source
	if source == "" {
		source = "yandex"
	}
	wait := 30 * time.Second
	if w, err := time.ParseDuration(sc.AttachWait); err == nil && w > 0 {
		wait = w
	}
	regions := []string{cc.Region}
	if cc.TerminalScope && cc.Region == "" {
		regions = nil
	}
	rcfg := TripsRunConfig{
		Source:         source,
		ChurnThreshold: sc.TripsChurnThreshold, MaxSpeedKmh: sc.TripsMaxSpeedKmh,
		CoverageGate: sc.CoverageGate, SoftScore: sc.CoverageSoftScore,
		Margin:      cfg.Verification.ScoreMargin,
		WaitForGate: wait, Force: cc.Force, DryRun: false, Regions: regions,
		TerminalScope: cc.TerminalScope,
		Flatten:       cc.Flatten,
		GapFill: GapFillConfig{
			DumpPath: sc.YandexDumpPath,
			Logger:   logger,
		},
		ParamsFor: DefaultStopTerminalParamsFor(cfg.Verification),
		ClassFor:  UrbanClassFor,
		Logger:    logger,
	}
	inputSHA := tripsInputSHA(cc)
	planID := ComputePlanID(LogicVersionID(), collectTripsConfigHash(cc), []string{inputSHA})
	rcfg.PlanID, rcfg.InputSHA, rcfg.Tag = planID, inputSHA, cc.Tag

	runID, err := BeginTripsRun(ctx, rst, planID, inputSHA, cc.Tag)
	if err != nil {
		return sum, err
	}
	res, err := RunTripsSync(ctx, st, rst, cc.Trips, rcfg)
	if err != nil {
		_ = FinishTripsRun(ctx, rst, runID, "dead", res)
		return res, err
	}
	if err := FinishTripsRun(ctx, rst, runID, "done", res); err != nil {
		return res, err
	}
	logger.Info("collect trips done", "run_id", runID, "routes", res.Routes, "full_rate", res.FullTripRate)
	return res, nil
}

func collectTripsConfigHash(cc CollectTripsConfig) string {
	raw, _ := json.Marshal(struct {
		Region string `json:"region"`
		Date   string `json:"date"`
	}{cc.Region, cc.Date})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func tripsInputSHA(cc CollectTripsConfig) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(cc.Trips)
	return hex.EncodeToString(h.Sum(nil))
}

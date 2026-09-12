// Overpass-сбор (issue #12): терминалы и рейсы без расписания из OSM.
// Терминалы идут тем же чанковым промоутом, что и Яндекс-скелет (score 0.4,
// IdentityOnly — single-source ручной режим); рейсы — flat без времён,
// attach-конвейер укладывает их в staging awaiting_times.
package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
)

// OverpassRecordsSource — источник записей Overpass (адаптер в caller'е;
// здесь — обезличенный контракт, sync не зависит от http).
type OverpassRecordsSource interface {
	StationsInBBox(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error)
}

// OverpassRoutesSource — маршрутные relation'ы региона (O-5): список
// identity-записей маршрутов bbox (ref, relation_id, стопы, геометрия).
type OverpassRoutesSource interface {
	FetchAllRouteRelations(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error)
}

// CollectOverpassConfig — параметры job'а overpass-сбора.
type CollectOverpassConfig struct {
	Region string
	MinLat float64
	MinLon float64
	MaxLat float64
	MaxLon float64
	Tag    string
}

// CollectOverpassSummary — сводка прогона (sync_runs.summary, экран
// «Прогоны»: источник виден явно, а не «ничего не написано»).
type CollectOverpassSummary struct {
	Kind         string   `json:"kind"`
	Source       string   `json:"source"`
	Region       string   `json:"region"`
	BBox         string   `json:"bbox"`
	BBoxRegional bool     `json:"bbox_regional"`
	StationsIn   int      `json:"stations_in"`
	TerminalsIn  int      `json:"terminals_in"`
	Written      int      `json:"written"`
	Review       int      `json:"review"`
	Chunks       int      `json:"chunks"`
	RoutesIn     int      `json:"routes_in"`
	Trips        int      `json:"trips"`
	RoutesDead   int      `json:"routes_dead"`
	RelationIDs  []string `json:"relation_ids,omitempty"`
}

// RunCollectOverpassSkeleton — терминальные станции региона из Overpass →
// чанковый промоут unverified-терминалов (тот же путь, что и Яндекс-скелет:
// координаты → канон score 0.4 IdentityOnly, без координат — review).
func RunCollectOverpassSkeleton(ctx context.Context, st store.Store, src OverpassRecordsSource, cc CollectOverpassConfig, logger *slog.Logger) (CollectOverpassSummary, int64, error) {
	sum := CollectOverpassSummary{Kind: "collect_overpass_skeleton", Source: "osm", Region: cc.Region,
		BBox: fmt.Sprintf("%.4f,%.4f,%.4f,%.4f", cc.MinLat, cc.MinLon, cc.MaxLat, cc.MaxLon), BBoxRegional: true}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "collect_overpass", "source", "osm", "region", cc.Region, "tag", cc.Tag)

	records, err := src.StationsInBBox(ctx, cc.MinLat, cc.MinLon, cc.MaxLat, cc.MaxLon)
	if err != nil {
		return sum, 0, fmt.Errorf("collect overpass: станции региона: %w", err)
	}
	sum.StationsIn = len(records)
	logger.Info("overpass stations fetched", "stations", sum.StationsIn, "bbox", sum.BBox)

	for i := range records {
		if records[i].Extra == nil {
			records[i].Extra = map[string]string{}
		}
		records[i].Extra["region"] = cc.Region
	}

	outcome := skeleton.JoinOutcome{Unverified: records}
	chunks := ChunkSkeleton(outcome, 0)
	sum.Chunks = len(chunks)
	sstore := skeletonStoreOf(st)
	if sstore == nil {
		return sum, 0, fmt.Errorf("collect overpass: стор без skeleton-методов")
	}
	planID := ComputePlanID(LogicVersionID(), overpassConfigHash(cc), []string{fmt.Sprintf("overpass:%s", cc.Region)})
	runID, err := BeginSkeletonRun(ctx, sstore, planID, fmt.Sprintf("overpass:%s:%s", cc.Region, sum.BBox), cc.Tag)
	if err != nil {
		return sum, 0, err
	}
	sr, ok := st.(skeletonChunkRunner)
	if !ok {
		// StageSkeletonRecords — только у Postgres-стора; чанковая
		// проводка без стейджинга допустима (memory-стор в тестах):
		// промоут идёт, staging-слой пропускается.
		sr = nil
	}
	if sr != nil {
		if err := sr.StageSkeletonRecords(ctx, runID, records); err != nil {
			logger.Warn("staging skipped", "error", err)
		}
	}
	for _, ch := range chunks {
		if err := ctx.Err(); err != nil {
			return sum, runID, err
		}
		if sr != nil {
			chunkID, err := sr.EnsureSyncChunk(ctx, runID, "terminal", ch.Key)
			if err != nil {
				return sum, runID, err
			}
			csum, err := PromoteSkeletonChunk(ctx, st, runID, ch)
			if err != nil {
				_ = sr.CompleteSyncChunk(ctx, chunkID, planID, "dead", err.Error())
				return sum, runID, err
			}
			if err := sr.CompleteSyncChunk(ctx, chunkID, planID, "done", ""); err != nil {
				return sum, runID, err
			}
			sum.Written += csum.Written
			sum.Review += csum.Review
		} else {
			csum, err := PromoteSkeletonChunk(ctx, st, runID, ch)
			if err != nil {
				return sum, runID, err
			}
			sum.Written += csum.Written
			sum.Review += csum.Review
		}
	}
	sum.TerminalsIn = sum.StationsIn
	raw, _ := json.Marshal(sum)
	if err := sstore.FinishSyncRun(ctx, runID, "done", string(raw)); err != nil {
		return sum, runID, err
	}
	logger.Info("collect overpass skeleton done", "run_id", runID, "stations_in", sum.StationsIn, "written", sum.Written, "review", sum.Review)
	return sum, runID, nil
}

func overpassConfigHash(cc CollectOverpassConfig) string {
	raw, _ := json.Marshal(struct {
		Region string  `json:"region"`
		MinLat float64 `json:"min_lat"`
		MinLon float64 `json:"min_lon"`
		MaxLat float64 `json:"max_lat"`
		MaxLon float64 `json:"max_lon"`
	}{cc.Region, cc.MinLat, cc.MinLon, cc.MaxLat, cc.MaxLon})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// FlattenOverpassRoute — identity-запись маршрута (O-5 AdaptedTripData из
// Raw) → FlatTrip без времён: стопы с osm-кодами и координатами, порядок
// relation. Времена — монополия Яндекса (§1.2): таких трип ждёт staging
// awaiting_times, но маршруты/терминалы/геометрия попадают в канон сразу.
func FlattenOverpassRoute(rec model.AdaptedRecord, region string) (*model.FlatTrip, error) {
	var data overpassTripData
	if err := json.Unmarshal(rec.Raw, &data); err != nil {
		return nil, fmt.Errorf("overpass route flatten: %w", err)
	}
	if len(data.Stops) < 2 {
		return nil, fmt.Errorf("overpass route flatten: %s — меньше двух остановок", rec.NameRu)
	}
	relID := ""
	for _, id := range rec.Identifiers {
		if id.CodeType == "osm_id" && strings.HasPrefix(id.Code, "relation/") {
			relID = strings.TrimPrefix(id.Code, "relation/")
			break
		}
	}
	stops := make([]model.FlatStop, 0, len(data.Stops))
	for _, s := range data.Stops {
		stop := model.FlatStop{
			StopID: strconv.FormatInt(s.OSMID, 10),
			Name:   s.Name,
			Region: region,
			Codes: []model.AdaptedIdentifier{
				{System: "osm", CodeType: "osm_id", Code: strconv.FormatInt(s.OSMID, 10)},
			},
		}
		if s.Lat != 0 || s.Lon != 0 {
			lat, lon := s.Lat, s.Lon
			stop.Lat, stop.Lon = &lat, &lon
		}
		stops = append(stops, stop)
	}
	first, last := stops[0], stops[len(stops)-1]
	trip := &model.FlatTrip{
		RouteNK:   "osm-" + data.Ref + "-" + relID,
		RouteReg:  data.Ref,
		Direction: "forward",
		ServiceID: overpassServiceID(relID),
		Run:       1,
		Period:    "",
		Carrier:   data.Operator,
		RouteFrom: first.Name,
		RouteTo:   last.Name,
		Stops:     stops,
		Weekdays:  nil,
	}
	return trip, nil
}

// overpassTripData — зеркало overpass.AdaptedTripData без зависимости
// internal/sync → internal/adapters (границы слоёв: sync выше adapters,
// но тип адаптера не должен протекать в контракт job'а; Raw — json-конверт).
type overpassTripData struct {
	Ref      string `json:"ref"`
	Operator string `json:"operator,omitempty"`
	Network  string `json:"network,omitempty"`
	Stops    []struct {
		OSMID int64   `json:"osm_id"`
		Lat   float64 `json:"lat,omitempty"`
		Lon   float64 `json:"lon,omitempty"`
		Role  string  `json:"role"`
		Name  string  `json:"name,omitempty"`
	} `json:"stops"`
}

func overpassServiceID(relID string) int64 {
	h := sha256.Sum256([]byte("overpass:relation/" + relID))
	return int64(binaryBeUint32(h[:4]) & 0x7fffffff)
}

func binaryBeUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// RunCollectOverpassTrips — маршруты региона из Overpass → flat без времён →
// attach-конвейер. Итог: staged awaiting_times (порядок+терминалы в каноне,
// времена добирает Яндекс позже) + coverage-информация по стопам.
func RunCollectOverpassTrips(ctx context.Context, st store.Store, rst TripsRunnerStore, src OverpassRoutesSource, sc config.Sync, vcfg config.Config, cc CollectOverpassConfig, force bool, logger *slog.Logger) (CollectOverpassSummary, TripsRunSummary, error) {
	sum := CollectOverpassSummary{Kind: "collect_overpass_routes", Source: "osm", Region: cc.Region,
		BBox: fmt.Sprintf("%.4f,%.4f,%.4f,%.4f", cc.MinLat, cc.MinLon, cc.MaxLat, cc.MaxLon), BBoxRegional: true}
	var tsum TripsRunSummary
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "collect_overpass_routes", "source", "osm", "region", cc.Region, "tag", cc.Tag)

	routeRecs, err := src.FetchAllRouteRelations(ctx, cc.MinLat, cc.MinLon, cc.MaxLat, cc.MaxLon)
	if err != nil {
		return sum, tsum, fmt.Errorf("collect overpass routes: маршрутные relation'ы: %w", err)
	}
	sum.RoutesIn = len(routeRecs)
	logger.Info("overpass route relations fetched", "routes", sum.RoutesIn, "bbox", sum.BBox)

	var trips []model.FlatTrip
	for _, rec := range routeRecs {
		if err := ctx.Err(); err != nil {
			return sum, tsum, err
		}
		trip, err := FlattenOverpassRoute(rec, cc.Region)
		if err != nil {
			logger.Warn("overpass route skipped", "name", rec.NameRu, "error", err)
			sum.RoutesDead++
			continue
		}
		trips = append(trips, *trip)
		if rel := strings.TrimPrefix(relationCodeOf(rec), "relation/"); rel != "" {
			sum.RelationIDs = append(sum.RelationIDs, rel)
		}
	}
	sum.Trips = len(trips)
	sort.Strings(sum.RelationIDs)

	tcfg := CollectTripsConfig{
		Region:  cc.Region,
		Date:    "",
		Tag:     cc.Tag,
		Offline: false,
		Force:   force,
		Trips:   trips,
		Flatten: model.FlattenStats{
			Schedules: 0,
			Runs:      len(trips),
			Trips:     len(trips),
		},
		Source: "osm",
	}
	tsum, err = RunCollectTrips(ctx, st, rst, sc, vcfg, tcfg, logger)
	if err != nil {
		return sum, tsum, err
	}
	return sum, tsum, nil
}

func relationCodeOf(rec model.AdaptedRecord) string {
	for _, id := range rec.Identifiers {
		if id.CodeType == "osm_id" {
			return id.Code
		}
	}
	return ""
}

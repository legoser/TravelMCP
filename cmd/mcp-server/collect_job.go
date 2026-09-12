// Job sync_collect_region: интерактивный сбор региона (ручной режим).
// kind=skeleton — Яндекс-дамп → unverified-терминалы; kind=trips —
// расписания терминальных станций (cache-first Rasp) → flat-рейсы → attach.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"travelmcp/internal/adapters/overpass"
	"travelmcp/internal/adapters/yandex"
	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/jobs"
	"travelmcp/internal/mcp"
	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	"travelmcp/internal/store"
	"travelmcp/internal/support/httpx"
	syncpkg "travelmcp/internal/sync"
)

type collectPayload struct {
	Kind         string   `json:"kind"`
	Regions      []string `json:"regions"`
	Region       string   `json:"region"`
	Date         string   `json:"date"`
	Transports   []string `json:"transports"`
	StationTypes []string `json:"station_types"`
	Stations     string   `json:"stations"`
	Offline      bool     `json:"offline"`
	Force        bool     `json:"force"`
	Tag          string   `json:"tag"`
	TerminalID   int64    `json:"terminal_id"`
	Transport    string   `json:"transport"`
}

// CollectRunner — зависимости job'а (инжектятся из main для тестируемости).
type CollectRunner struct {
	Store  store.Store
	Config *config.Config
	Logger *slog.Logger
}

// HandleCollectRegion — обработчик jobs{type=sync_collect_region}.
// Payload: {kind: skeleton|trips, regions/region, date, transports,
// station_types, offline, tag}.
func (cr *CollectRunner) HandleCollectRegion(ctx context.Context, job store.JobRow) error {
	l := cr.Logger.With("job_id", job.ID)
	var p collectPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("collect payload: %w", err)
	}
	switch p.Kind {
	case "skeleton":
		return cr.runSkeleton(ctx, p, l)
	case "trips":
		return cr.runTrips(ctx, job.ID, p, l)
	case "routes":
		return cr.runRoutes(ctx, p, l)
	default:
		return fmt.Errorf("collect: kind обязан быть skeleton|trips|routes, получен %q", p.Kind)
	}
}

func (cr *CollectRunner) runSkeleton(ctx context.Context, p collectPayload, logger *slog.Logger) error {
	cfg := cr.Config
	regions := p.Regions
	if len(regions) == 0 && p.Region != "" {
		regions = []string{p.Region}
	}
	tag := p.Tag
	if tag == "" {
		tag = "collect-" + joinDash(regions)
	}
	stationTypes := stringSet(p.StationTypes)
	if len(stationTypes) == 0 {
		stationTypes = skeleton.StationClasses
	}
	transports := stringSet(p.Transports)
	if len(transports) == 0 {
		transports = map[string]bool{"bus": true, "train": true, "flight": true}
	}
	sum, err := syncpkg.RunCollectSkeleton(ctx, cr.Store, cfg.Sync, syncpkg.CollectSkeletonConfig{
		Regions:      regions,
		Transports:   transports,
		StationTypes: stationTypes,
		YandexPath:   cfg.Sync.YandexDumpPath,
		Tag:          tag,
		Offline:      p.Offline,
	}, logger)
	if err != nil {
		return err
	}
	logger.Info("collect skeleton complete", "in", sum.In, "written", sum.Written, "review", sum.Review, "chunks", sum.Chunks)
	mcp.BumpCanonVersion()
	return nil
}

// jobCancelChecker — живой опрос отмены running-задания (issue #22):
// отмена из UI ставит jobs.state='cancelled'; конвейер сбора
// проверяет на каждой станции и останавливается — кэш и собранные
// до отмены данные валидны.
type jobCancelChecker interface {
	JobCancelled(ctx context.Context, id int64) (bool, error)
}

// cancelledErr — маркер: воркер не делает retry/dead, задание уже
// в state='cancelled'.
func cancelledErr(logger *slog.Logger, jobID int64) error {
	logger.Warn("collect отменён оператором", "job_id", jobID)
	return jobs.ErrCancelled
}

func (cr *CollectRunner) runTrips(ctx context.Context, jobID int64, p collectPayload, logger *slog.Logger) error {
	cfg := cr.Config
	if p.Region == "" && p.TerminalID <= 0 {
		return fmt.Errorf("collect trips: region обязателен (или terminal_id для точечного сбора)")
	}
	date := p.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	tag := p.Tag
	if tag == "" {
		tag = "collect-trips-" + joinDash([]string{p.Region})
	}
	stationTypes := stationTypesOf(p)
	rasp := yandex.NewRasp(*cfg, httpx.New(logger, "yandex-rasp"), cfg.Yandex.RaspCacheDir, quotaOf(cr.Store), p.Offline)

	// Точечный прогон: один терминал из канона (yandex_code), транспорт
	// фильтрует нитки. Регион не нужен — терминал уже в каноне.
	var yan []model.AdaptedRecord
	if p.TerminalID > 0 {
		rec, termRegion, err := cr.terminalStation(ctx, p.TerminalID, p.Transport)
		if err != nil {
			return err
		}
		yan = []model.AdaptedRecord{*rec}
		if p.Region == "" {
			p.Region = termRegion
		}
		if tag == "collect-trips-" {
			tag = fmt.Sprintf("collect-trips-term-%d", p.TerminalID)
		}
		logger.Info("collect trips: точечный терминал", "terminal_id", p.TerminalID, "code", rec.PrimaryCode(), "transport", p.Transport)
	}

	// Источник терминальных станций: yandex-дамп (по умолчанию) или
	// Overpass (bbox региона из sync.region_bboxes, fallback sync.bbox —
	// с явным логом; путь cache+quota, offline запрещён). Overpass-станции
	// без yandex_code: резолв кода по координате (nearest, cache-first).
	if p.TerminalID <= 0 {
		switch p.Stations {
		case "overpass":
			if p.Offline {
				return fmt.Errorf("collect trips: overpass-станции требуют сеть (offline не поддерживается)")
			}
			bbox, regional, err := regionBBoxOf(cfg, p.Region)
			if err != nil {
				return err
			}
			op := overpass.New(*cfg, httpx.New(logger, "overpass"))
			cached := geocoder.NewCachedStationsProvider(op, geoCacheFor(cr.Store), geoQuotaOf(cr.Store), store.DefaultQuotaLimit)
			yan, err = cached.StationsInBBox(ctx, bbox.MinLat, bbox.MinLon, bbox.MaxLat, bbox.MaxLon)
			if err != nil {
				return fmt.Errorf("collect trips: overpass: %w", err)
			}
			logger.Info("collect trips: станции из overpass", "stations", len(yan),
				"bbox", bbox.String(), "bbox_regional", regional, "region", p.Region)
		default:
			var err error
			yan, err = skeleton.YandexDumpSource{Path: cfg.Sync.YandexDumpPath}.Load()
			if err != nil {
				return err
			}
			yan = skeleton.FilterYandexRecords(yan, []string{p.Region}, map[string]bool{"bus": true, "train": true, "flight": true}, stationTypes)
		}
	}
	if len(yan) == 0 {
		return fmt.Errorf("collect trips: нет терминальных станций региона %q", p.Region)
	}
	logger.Info("collect trips: станции региона", "region", p.Region, "stations", len(yan), "date", date, "offline", p.Offline)

	// Атомарный конвейер: станция → schedule → её нитки → flatten сразу.
	// Каждый API-ответ оседает в кэш до следующего шага: обрыв на любой
	// точке не сжигает квоты — собранное переиспользуется из кэша.
	var trips []model.FlatTrip
	stats := syncpkg.RaspCollectStats{}
	seenUID := map[string]bool{}
	cancelCheck, hasCancel := cr.Store.(jobCancelChecker)
	for _, rec := range yan {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if hasCancel {
			if cancelled, err := cancelCheck.JobCancelled(ctx, jobID); err == nil && cancelled {
				return cancelledErr(logger, jobID)
			}
		}
		code := rec.PrimaryCode()
		if code == "" {
			// Overpass-станция без yandex_code: резолв по координате
			// (nearest_stations, cache-first, та же квота).
			if rec.Lat == nil || rec.Lon == nil {
				continue
			}
			resolved, err := rasp.NearestStation(ctx, *rec.Lat, *rec.Lon)
			if err != nil {
				logger.Warn("nearest station unresolved, skipped", "name", rec.NameRu, "error", err)
				continue
			}
			code = resolved
		}
		sched, err := rasp.Schedule(ctx, code, date)
		if err != nil {
			logger.Warn("schedule unavailable, station skipped", "station", code, "error", err)
			continue
		}
		stats.Schedules++
		for _, it := range sched.Schedule {
			uid := it.Thread.UID
			if uid == "" || seenUID[uid] {
				continue
			}
			// Точечный прогон: транспорт терминала фильтрует нитки
			// (несколько типов на терминале — пользователь выбирает).
			if p.Transport != "" && !yandex.TransportCompatibleRasp(it.Thread.TransportType, p.Transport) {
				continue
			}
			// Сдвиг зоны нитки из её schedule-события (+07:00):
			// времена thread местные, канон хранит UTC (§3.13).
			// Станции сбора идут по алфавиту кода — зона у всех одна
			// (регион пилота), но считаем per-нитку: честнее при
			// смешанных зонах в одном прогоне.
			tzShiftMin := 0
			if shift, ok := yandex.ParseRaspTzShift(it.Departure); ok {
				tzShiftMin = shift
			}
			seenUID[uid] = true
			thread, err := rasp.Thread(ctx, uid)
			if err != nil {
				logger.Warn("thread unavailable, skipped", "uid", uid, "error", err)
				stats.ThreadsDead++
				continue
			}
			stats.Threads++
			res := yandex.FlattenRaspThread(thread, yandex.RaspFlattenConfig{Region: p.Region, TzShiftMin: tzShiftMin})
			switch res.State {
			case "promoted":
				trips = append(trips, *res.Trip)
				stats.Trips++
			case "restricted_days":
				stats.Restricted++
			case "no_times":
				stats.NoTimes++
			default:
				stats.Empty++
			}
		}
	}
	rs := rasp.Stats()
	logger.Info("collect trips: flat ready", "trips", len(trips),
		"restricted", stats.Restricted, "no_times", stats.NoTimes, "empty", stats.Empty,
		"threads_dead", stats.ThreadsDead,
		"rasp_schedule_cache", rs.ScheduleCache, "rasp_schedule_api", rs.ScheduleAPI,
		"rasp_thread_cache", rs.ThreadCache, "rasp_thread_api", rs.ThreadAPI,
		"rasp_quota_blocked", rs.QuotaBlocked)
	if len(trips) == 0 {
		return fmt.Errorf("collect trips: годных рейсов нет (кэш %s пуст для %s или квота исчерпана)", cfg.Yandex.RaspCacheDir, date)
	}

	rst, ok := cr.Store.(syncpkg.TripsRunnerStore)
	if !ok {
		return fmt.Errorf("collect trips: стор без trips-методов")
	}
	fstats := model.FlattenStats{
		Schedules:       stats.Schedules,
		Runs:            stats.Threads,
		Trips:           stats.Trips,
		RestrictedTrips: stats.Restricted + stats.NoTimes + stats.Empty,
	}
	sum, err := syncpkg.RunCollectTrips(ctx, cr.Store, rst, cfg.Sync, *cfg, syncpkg.CollectTripsConfig{
		Region:  p.Region,
		Date:    date,
		Tag:     tag,
		Offline: p.Offline,
		Force:   p.Force,
		Trips:   trips,
		Flatten: fstats,
		Source:  "yandex",
		// Точечный прогон: гейты канона не должны срабатывать на
		// «исчезновение» всех остальных рейсов.
		TerminalScope: p.TerminalID > 0,
	}, logger)
	if err != nil {
		return err
	}
	logger.Info("collect trips complete", "routes", sum.Routes, "full_rate", sum.FullTripRate,
		"promoted", lenSumByRoute(sum, "Promoted"), "staged", lenSumByRoute(sum, "Staged"))
	mcp.BumpCanonVersion()
	return nil
}

func lenSumByRoute(sum syncpkg.TripsRunSummary, field string) int {
	n := 0
	for _, o := range sum.ByRoute {
		switch field {
		case "Promoted":
			n += o.Promoted
		case "Staged":
			n += o.Staged
		}
	}
	return n
}

// runRoutes — kind=routes (issue #12): терминальные станции + маршрутные
// relation'ы региона из Overpass, без расписания. Терминалы — чанковый
// промоут (IdentityOnly 0.4), маршруты — flat без времён → staging
// awaiting_times (времена добирает Яндекс позже). Сеть обязательна.
func (cr *CollectRunner) runRoutes(ctx context.Context, p collectPayload, logger *slog.Logger) error {
	cfg := cr.Config
	if p.Region == "" {
		return fmt.Errorf("collect routes: region обязателен")
	}
	if p.Offline {
		return fmt.Errorf("collect routes: overpass-сбор требует сеть (offline не поддерживается)")
	}
	bbox, regional, err := regionBBoxOf(cfg, p.Region)
	if err != nil {
		return err
	}
	tag := p.Tag
	if tag == "" {
		tag = "collect-routes-" + p.Region
	}
	op := overpass.New(*cfg, httpx.New(logger, "overpass"))
	stations := geocoder.NewCachedStationsProvider(op, geoCacheFor(cr.Store), geoQuotaOf(cr.Store), store.DefaultQuotaLimit)
	// Маршрутные relation'ы — тот же путь cache+quota (§3.7): сырой адаптер
	// в sync не передаётся, bbox-запросы идут через обёртку.
	routesSrc := geocoder.NewCachedRoutesProvider(op, geoCacheFor(cr.Store), geoQuotaOf(cr.Store), store.DefaultQuotaLimit)

	cc := syncpkg.CollectOverpassConfig{
		Region: p.Region,
		MinLat: bbox.MinLat, MinLon: bbox.MinLon, MaxLat: bbox.MaxLat, MaxLon: bbox.MaxLon,
		Tag: tag,
	}

	ssum, _, err := syncpkg.RunCollectOverpassSkeleton(ctx, cr.Store, stations, cc, logger)
	if err != nil {
		return err
	}
	logger.Info("collect routes: терминалы региона собраны", "source", "osm",
		"stations", ssum.StationsIn, "written", ssum.Written, "review", ssum.Review,
		"bbox", ssum.BBox, "bbox_regional", regional)

	rst, ok := cr.Store.(syncpkg.TripsRunnerStore)
	if !ok {
		return fmt.Errorf("collect routes: стор без trips-методов")
	}
	rsum, tsum, err := syncpkg.RunCollectOverpassTrips(ctx, cr.Store, rst, routesSrc, cfg.Sync, *cfg, cc, p.Force, logger)
	if err != nil {
		return err
	}
	logger.Info("collect routes complete", "source", "osm",
		"routes_in", rsum.RoutesIn, "trips", rsum.Trips, "routes_dead", rsum.RoutesDead,
		"staged", lenSumByRoute(tsum, "Staged"), "promoted", lenSumByRoute(tsum, "Promoted"),
		"bbox", rsum.BBox, "bbox_regional", regional,
		"relations", len(rsum.RelationIDs))
	if rsum.Trips == 0 {
		return fmt.Errorf("collect routes: маршрутов нет (bbox %s пуст или маршрутные relation'ы не размечены)", rsum.BBox)
	}
	mcp.BumpCanonVersion()
	return nil
}

// regionBBoxOf — bbox региона: карта region_bboxes → fallback глобальный
// bbox пилота (issue #12: запуск «Республика Алтай» не должен молча брать
// bbox Кузбасса — fallback всегда явно виден в логах).
func regionBBoxOf(cfg *config.Config, region string) (overpass.BBox, bool, error) {
	if region == "" {
		return overpass.BBox{}, false, fmt.Errorf("collect: region обязателен для overpass-сбора")
	}
	spec, regional := cfg.Sync.RegionBBox(region)
	if spec == "" {
		return overpass.BBox{}, false, fmt.Errorf("collect: нет bbox для региона %q (sync.bbox пуст)", region)
	}
	bbox, err := parseBBox(spec)
	if err != nil {
		return overpass.BBox{}, false, fmt.Errorf("collect: bbox региона %q: %w", region, err)
	}
	if !regional {
		slog.Warn("collect overpass: регион не в карте region_bboxes, bbox пилота может не покрывать регион",
			"region", region, "bbox", spec)
	}
	return bbox, regional, nil
}

// geoCacheFor — межпрогонный кэш гео-запросов (geocode_cache в Postgres,
// in-memory для остальных) + обёртка под контракт geocoder.GeoCacheStore.
func geoCacheFor(st store.Store) geocoder.GeoCacheStore {
	if gc, ok := st.(interface {
		GetGeoCache(ctx context.Context, key string) (geocoder.GeoCacheEntry, bool)
		SetGeoCache(ctx context.Context, key string, e geocoder.GeoCacheEntry) error
	}); ok {
		return &dbGeoCache{st: gc}
	}
	return geocoder.NewMapGeoCacheStore(0)
}

type dbGeoCache struct {
	st interface {
		GetGeoCache(ctx context.Context, key string) (geocoder.GeoCacheEntry, bool)
		SetGeoCache(ctx context.Context, key string, e geocoder.GeoCacheEntry) error
	}
}

func (c *dbGeoCache) Get(key string) (geocoder.GeoCacheEntry, bool) {
	return c.st.GetGeoCache(context.Background(), key)
}

func (c *dbGeoCache) Set(key string, e geocoder.GeoCacheEntry) {
	_ = c.st.SetGeoCache(context.Background(), key, e)
}

func stringSet(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, s := range in {
		if s != "" {
			out[s] = true
		}
	}
	return out
}

// stationTypesOf — классы станций для сбора рейсов: payload-набор или
// терминальный дефолт (городские bus_stop/stop в пилот не входят).
func stationTypesOf(p collectPayload) map[string]bool {
	if st := stringSet(p.StationTypes); st != nil {
		return st
	}
	return skeleton.StationClasses
}

// terminalStation — канон-терминал как станция для точечного сбора
// расписания. Возвращает AdaptedRecord с yandex_code и регион (из тегов
// канона; пустой — если не enriched). Транспорт не подмешивается в
// record: им фильтруются нитки в конвейере.
func (cr *CollectRunner) terminalStation(ctx context.Context, terminalID int64, transport string) (*model.AdaptedRecord, string, error) {
	lister, ok := cr.Store.(interface {
		ListTerminalCodes(ctx context.Context, terminalID int64, system string) ([]model.AdaptedIdentifier, error)
	})
	if !ok {
		return nil, "", fmt.Errorf("collect trips: стор без identifier-методов")
	}
	codes, err := lister.ListTerminalCodes(ctx, terminalID, "yandex")
	if err != nil {
		return nil, "", fmt.Errorf("collect trips: идентификаторы терминала %d: %w", terminalID, err)
	}
	yandexCode := ""
	for _, c := range codes {
		if c.CodeType == "yandex_code" && c.Code != "" {
			yandexCode = c.Code
			break
		}
	}
	if yandexCode == "" {
		return nil, "", fmt.Errorf("collect trips: у терминала %d нет yandex_code — точечный сбор невозможен", terminalID)
	}
	getter, ok := cr.Store.(interface {
		GetTerminal(ctx context.Context, id int64) (map[string]any, error)
		GetTerminalTags(ctx context.Context, id int64) (map[string]string, error)
	})
	if !ok {
		return nil, "", fmt.Errorf("collect trips: стор без terminal-методов")
	}
	tm, err := getter.GetTerminal(ctx, terminalID)
	if err != nil {
		return nil, "", fmt.Errorf("collect trips: терминал %d: %w", terminalID, err)
	}
	tags, _ := getter.GetTerminalTags(ctx, terminalID)
	rec := &model.AdaptedRecord{
		Kind:   model.AdaptedTerminal,
		NameRu: strOrEmpty(tm["name"]),
		Source: "yandex",
		Identifiers: []model.AdaptedIdentifier{
			{System: "yandex", CodeType: "yandex_code", Code: yandexCode},
		},
		Extra: map[string]string{},
	}
	if lat, ok := tm["lat"].(float64); ok {
		l := lat
		rec.Lat = &l
	}
	if lon, ok := tm["lon"].(float64); ok {
		l := lon
		rec.Lon = &l
	}
	region := tags["region"]
	if region == "" {
		if stg, ok := cr.Store.(interface {
			TerminalStagingRegion(ctx context.Context, terminalID int64) string
		}); ok {
			region = stg.TerminalStagingRegion(ctx, terminalID)
		}
	}
	return rec, region, nil
}

func strOrEmpty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func joinDash(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "-"
		}
		out += p
	}
	return out
}

func quotaOf(st store.Store) yandex.QuotaFunc {
	return func(ctx context.Context, provider string, limit int) (bool, int, error) {
		return st.TryConsumeQuota(ctx, provider, limit)
	}
}

// geoQuotaOf — та же квота под контракт geocoder (именованные типы
// совпадают по сигнатуре, но не конвертируются напрямую).
func geoQuotaOf(st store.Store) geocoder.QuotaFunc {
	return func(ctx context.Context, provider string, limit int) (bool, int, error) {
		return st.TryConsumeQuota(ctx, provider, limit)
	}
}

// parseBBox — "minLat,minLon,maxLat,maxLon" (конфиг sync.bbox).
func parseBBox(s string) (overpass.BBox, error) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) != 4 {
		return overpass.BBox{}, fmt.Errorf("ожидалось 4 числа через запятую, получено %d", len(parts))
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return overpass.BBox{}, fmt.Errorf("координата %d: %w", i+1, err)
		}
		v[i] = f
	}
	return overpass.BBox{MinLat: v[0], MinLon: v[1], MaxLat: v[2], MaxLon: v[3]}, nil
}

// registerCollectWorker — монтаж job'а в воркер (вызывается из newJobsWorker).
func registerCollectWorker(w *jobs.Worker, st store.Store, cfg *config.Config, logger *slog.Logger) {
	cr := &CollectRunner{Store: st, Config: cfg, Logger: logger}
	w.Register("sync_collect_region", cr.HandleCollectRegion)
}

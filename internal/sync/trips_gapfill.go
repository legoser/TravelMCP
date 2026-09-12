package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
	"travelmcp/internal/verification"
)

// GapFillSummary — итог автодобавления терминалов из дампа Яндекса.
type GapFillSummary struct {
	// DumpStations — станций в дампе (после фильтра терминальных классов).
	DumpStations int `json:"dump_stations"`
	// MissingEndpoints — конечных стопов (first/last) без матча в каноне.
	MissingEndpoints int `json:"missing_endpoints"`
	// Added — терминалов создано в каноне (confidence 0.4, IdentityOnly).
	Added int `json:"added"`
	// Reused — стопов закрыто уже существующим терминалом (код нашёлся
	// в каноне, но не попал в skeleton-выборку прогонa).
	Reused int `json:"reused"`
	// SkippedNoDump — стопов без кода/без станции в дампе: остаются
	// в skeleton_gap (операторский флоу §5.4).
	SkippedNoDump int `json:"skipped_no_dump"`
}

// GapFillConfig — параметры автодобавления (issue #16).
type GapFillConfig struct {
	// DumpPath — файл дампа станций Яндекса (cfg.Sync.YandexDumpPath).
	DumpPath string
	// Score — confidence создаваемого терминала (O-10: unverified 0.4).
	Score float64
	// Logger — логгер прогона.
	Logger *slog.Logger
}

// gapFillScore — confidence автодобавления (O-10): ниже verified-порога,
// выше seed-уровня; стопы такого терминала получают is_provisional.
const gapFillScore = 0.4

// LoadGapFillDump — загрузка дампа Яндекса, срез только терминальных
// классов (городские bus_stop/stop/platform не создают междугородних
// терминалов, пилот §5.4). Дамп — локальный файл, без квот.
func LoadGapFillDump(path string) ([]model.AdaptedRecord, error) {
	recs, err := skeleton.YandexDumpSource{Path: path}.Load()
	if err != nil {
		return nil, fmt.Errorf("gapfill yandex dump: %w", err)
	}
	return skeleton.FilterYandexRecords(recs, nil, nil, skeleton.StationClasses), nil
}

// GapFillEndpoints — до-attach шаг (issue #16): конечные стопы (first/
// last) рейсов среза, не сматчивающиеся к терминалам канона, закрываются
// станцией из дампа Яндекса по точному коду (yandex_code). Найденная
// станция с координатами становится каноническим терминалом (O-10:
// score 0.4, IdentityOnly) и добавляется в пул attach. Промоушен терминала
// публикует terminal.created в outbox (§4.2) — подписчик перепроверит
// staging-рейсы региона.
//
// Дубликаты исключены тремя фильтрами: (1) матчинг только по коду —
// name-guessing не применяется, тёзки не создаются; (2) дедуп по коду
// против канона (ListTerminalIDByCode) и против пула прогонa; (3) станции
// без координат в канон не идут (проверяемая геометрия — обязательное
// условие O-10, иначе терминал ждал бы ручной верификации).
func GapFillEndpoints(ctx context.Context, st TripsRunnerStore, terms []AttachTerminal, trips []model.FlatTrip, cfg GapFillConfig) (GapFillSummary, []AttachTerminal, error) {
	var sum GapFillSummary
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("step", "gapfill_endpoints")

	// Срез endpoint'ов, которых канон не матчит.
	idx := buildMatchIndex(terms)
	missing := missingEndpoints(trips, terms, idx)
	if len(missing) == 0 {
		return sum, terms, nil
	}
	sum.MissingEndpoints = len(missing)

	recs, err := LoadGapFillDump(cfg.DumpPath)
	if err != nil {
		// Дамп — инфраструктурная зависимость сбора, а не опциональный
		// источник: отсутствие файла = авто-добавление выключено,
		// конвейер продолжает работать как раньше (§5.4 операторский флоу).
		logger.Warn("gapfill: дамп Яндекса недоступен, автодобавление пропущено", "error", err, "path", cfg.DumpPath)
		return sum, terms, nil
	}
	sum.DumpStations = len(recs)

	byCode := map[string]model.AdaptedRecord{}
	for _, r := range recs {
		for _, id := range r.Identifiers {
			if id.Code != "" {
				byCode[id.System+"|"+id.Code] = r
			}
		}
	}

	seenCode := map[string]bool{}
	for _, t := range terms {
		for _, id := range t.Codes {
			seenCode[id.System+"|"+id.Code] = true
		}
	}

	score := cfg.Score
	if score <= 0 || score >= config.Defaults().Verification.ConfidenceThreshold {
		score = gapFillScore
	}
	now := time.Now()
	for _, stop := range missing {
		if err := ctx.Err(); err != nil {
			return sum, terms, err
		}
		rec, ok := stopByCode(stop.Codes, byCode)
		if !ok || !rec.HasCoords() {
			sum.SkippedNoDump++
			continue
		}
		key := ""
		for _, id := range rec.Identifiers {
			if id.Code != "" {
				k := id.System + "|" + id.Code
				if !seenCode[k] {
					key = k
				}
				seenCode[k] = true
			}
		}
		_ = key // ключ помечен занятым выше; создание ниже идемпотентно по коду
		row := store.TerminalRow{
			Lat: *rec.Lat, Lon: *rec.Lon,
			TransportTypes:   splitTransport(recordTransport(rec)),
			ObjectType:       recordObjectType(rec),
			EnrichmentStatus: string(skeleton.IdentityOnly),
		}
		// Дедуп по внешнему коду: если терминал уже есть в каноне, но
		// вне skeleton-выборки — берём его ID, дубликат не создаём.
		for _, id := range rec.Identifiers {
			if existing, ok := st.ListTerminalIDByCode(ctx, id.System, id.Code); ok {
				row.ID = existing
				break
			}
		}
		id, err := st.UpsertTerminal(ctx, row, map[string]string{"ru": rec.NameRu}, rec.Identifiers)
		if err != nil {
			return sum, terms, fmt.Errorf("gapfill upsert terminal %s: %w", rec.NameRu, err)
		}
		if v := recordExtra(rec, "settlement"); v != "" {
			if err := st.SetTerminalTag(ctx, id, "settlement", v); err != nil {
				return sum, terms, err
			}
		}
		geomSource := geomWinnerSource(rec)
		for _, attr := range []store.AttributeStateRow{
			{EntityType: "terminal", EntityID: id, Field: "geom", Value: fmt.Sprintf("%v,%v", *rec.Lat, *rec.Lon), Source: geomSource, Confidence: score, Origin: "live"},
			{EntityType: "terminal", EntityID: id, Field: "name_ru", Value: rec.NameRu, Source: geomSource, Confidence: score, Origin: "live"},
		} {
			if err := st.UpsertAttributeState(ctx, attr); err != nil {
				return sum, terms, err
			}
		}
		if err := st.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: id, Source: "yandex", Confidence: score, ObservedAt: now, Channel: model.ChannelLocalFile}); err != nil {
			return sum, terms, err
		}
		terms = append(terms, AdaptedRecordToAttachTerminal(id, rec, false))
		if row.ID != 0 {
			sum.Reused++
		} else {
			sum.Added++
			if ts, ok := st.(TripsStore); ok {
				if err := PublishTerminalCreated(ctx, ts, id, recordExtra(rec, "region")); err != nil {
					logger.Warn("gapfill: terminal.created не опубликован", "terminal_id", id, "error", err)
				}
			}
		}
		logger.Info("gapfill: терминал конечного стопа добавлен из дампа",
			"stop_id", stop.StopID, "stop_name", stop.Name,
			"terminal_id", id, "terminal_name", rec.NameRu,
			"settlement", recordExtra(rec, "settlement"),
			"reused", row.ID != 0)
	}
	if sum.Added > 0 || sum.Reused > 0 || sum.SkippedNoDump > 0 {
		logger.Info("gapfill: итог",
			"missing_endpoints", sum.MissingEndpoints, "added", sum.Added,
			"reused", sum.Reused, "skipped_no_dump", sum.SkippedNoDump,
			"dump_stations", sum.DumpStations)
	}
	return sum, terms, nil
}

// missingEndpoints — конечные стопы (first/last) рейсов, у которых нет
// верифицируемого матча в пуле терминалов: матчинг тот же, что в
// matchStops (ScorePair), но без побочных эффектов.
func missingEndpoints(trips []model.FlatTrip, terms []AttachTerminal, idx *matchIndex) []model.FlatStop {
	classFor := func(string) model.DensityClass { return model.DensityUrban }
	params := verification.DefaultStopTerminalParams(config.Verification{}, model.DensityUrban)
	out := []model.FlatStop{}
	seen := map[string]bool{}
	for _, ft := range trips {
		if len(ft.Stops) == 0 {
			continue
		}
		ends := []model.FlatStop{ft.Stops[0], ft.Stops[len(ft.Stops)-1]}
		for _, s := range ends {
			if seen[s.StopID] {
				continue
			}
			seen[s.StopID] = true
			pool := idx.candidates(s)
			if len(pool) == 0 {
				out = append(out, s)
				continue
			}
			cands := make([]verification.PairItem, 0, len(pool))
			for _, ti := range pool {
				cands = append(cands, PairItemFromTerminal(terms[ti]))
			}
			stop := PairItemFromStop(s.Name, s.Lat, s.Lon, "", "yandex", s.Codes)
			_, d, _ := verification.MatchStopToTerminal(stop, cands, classFor(""), params)
			if d != verification.DecisionVerified {
				out = append(out, s)
			}
		}
	}
	return out
}

func stopByCode(codes []model.AdaptedIdentifier, byCode map[string]model.AdaptedRecord) (model.AdaptedRecord, bool) {
	for _, id := range codes {
		if id.Code == "" {
			continue
		}
		if rec, ok := byCode[id.System+"|"+id.Code]; ok {
			return rec, true
		}
	}
	return model.AdaptedRecord{}, false
}

// AdaptedRecordToAttachTerminal — конвертация dump-станции в элемент
// attach-пула (после создания терминала).
func AdaptedRecordToAttachTerminal(id int64, rec model.AdaptedRecord, geomFinalized bool) AttachTerminal {
	return AttachTerminal{
		ID: id, Name: rec.NameRu, Lat: rec.Lat, Lon: rec.Lon,
		Settlement: recordExtra(rec, "settlement"),
		Transport:  recordTransport(rec),
		Source:     rec.Source, Codes: rec.Identifiers,
		GeomFinalized: geomFinalized,
	}
}

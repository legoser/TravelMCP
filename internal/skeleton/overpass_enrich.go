package skeleton

import (
	"context"
	"log/slog"

	"travelmcp/internal/geocoder"
	"travelmcp/internal/model"
)

// Локальные значения enrich-стадии (не конфиг): радиус поиска остановок
// вокруг unverified-записи, потолок обрабатываемых точек за прогон и score
// восстановленных записей (ниже verified-порога 0.6 — такие записи идут
// в канон как IdentityOnly, а не verified).
const (
	defaultRadiusM   = 500
	defaultMaxPoints = 200
	enrichedScore    = 0.75
)

// mergeIdentifiers — идентификаторы исходной unverified-записи (например
// yandex_code) обязаны переживать Overpass-апгрейд: без них терминал
// теряет точный code-match на attach рейсов. Дедуп по system|code_type|code.
func mergeIdentifiers(rec, osmRec model.AdaptedRecord) []model.AdaptedIdentifier {
	seen := map[string]bool{}
	out := make([]model.AdaptedIdentifier, 0, len(rec.Identifiers)+len(osmRec.Identifiers))
	for _, id := range rec.Identifiers {
		k := id.System + "|" + id.CodeType + "|" + id.Code
		if id.Code != "" && !seen[k] {
			seen[k] = true
			out = append(out, id)
		}
	}
	for _, id := range osmRec.Identifiers {
		k := id.System + "|" + id.CodeType + "|" + id.Code
		if id.Code != "" && !seen[k] {
			seen[k] = true
			out = append(out, id)
		}
	}
	return out
}

// extraOr — значение из OSM-записи, иначе из исходной (settlement/region
// Яндекс знает всегда, OSM — не всегда).
func extraOr(osmRec, rec model.AdaptedRecord, key string) string {
	if osmRec.Extra != nil && osmRec.Extra[key] != "" {
		return osmRec.Extra[key]
	}
	if rec.Extra != nil {
		return rec.Extra[key]
	}
	return ""
}

func OverpassEnrich(ctx context.Context, outcome *JoinOutcome, provider *geocoder.CachedStationsProvider, maxPoints int, logger *slog.Logger) int {
	if provider == nil {
		return 0
	}
	if maxPoints <= 0 {
		maxPoints = defaultMaxPoints
	}
	logger = logger.With("step", "overpass_enrich")

	upgrades := make([]JoinedRecord, 0, len(outcome.Unverified))
	recovered := 0
	skippedNoCoords := 0
	skippedErr := 0
	calls := 0
	recoveredIdx := make([]bool, len(outcome.Unverified))

	for i, rec := range outcome.Unverified {
		if i >= maxPoints {
			break
		}
		if !rec.HasCoords() {
			skippedNoCoords++
			continue
		}

		if err := ctx.Err(); err != nil {
			logger.Warn("context cancelled", "recovered", recovered)
			break
		}

		nearby, err := provider.StationsAround(ctx, *rec.Lat, *rec.Lon, defaultRadiusM)
		calls++
		if err != nil {
			skippedErr++
			logger.Debug("StationsAround failed", "name", rec.NameRu, "error", err)
			continue
		}
		if len(nearby) == 0 {
			continue
		}

		best := nearby[0]
		merged := model.AdaptedRecord{
			Kind:        model.AdaptedTerminal,
			NameRu:      best.NameRu,
			NameEn:      best.NameEn,
			Lat:         best.Lat,
			Lon:         best.Lon,
			Source:      "osm",
			Identifiers: mergeIdentifiers(rec, best),
			Extra: map[string]string{
				"transport_type": extraOr(best, rec, "transport_type"),
				"object_type":    best.Extra["object_type"],
				"osm_kind":       best.Extra["osm_kind"],
				"enrich_source":  "overpass",
				"yandex_name":    rec.NameRu,
			},
		}
		for _, k := range []string{"settlement", "region"} {
			if v := extraOr(best, rec, k); v != "" {
				merged.Extra[k] = v
			}
		}
		upgrades = append(upgrades, JoinedRecord{
			Record:     merged,
			Score:      enrichedScore,
			Enrichment: IdentityOnly,
		})
		recoveredIdx[i] = true
		recovered++
	}

	if recovered == 0 {
		logger.Info("overpass enrich done", "recovered", 0, "skipped_no_coords", skippedNoCoords, "skipped_err", skippedErr)
		return 0
	}

	outcome.Canon = append(outcome.Canon, upgrades...)

	keep := make([]model.AdaptedRecord, 0, len(outcome.Unverified)-recovered)
	for i, rec := range outcome.Unverified {
		if recoveredIdx[i] {
			continue
		}
		keep = append(keep, rec)
	}
	outcome.Unverified = keep

	logger.Info("overpass enrich done",
		"recovered", recovered,
		"overpass_calls", calls,
		"skipped_no_coords", skippedNoCoords,
		"skipped_err", skippedErr,
		"canon_after", len(outcome.Canon),
		"unverified_after", len(outcome.Unverified),
	)
	return recovered
}

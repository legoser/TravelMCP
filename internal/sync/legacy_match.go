package sync

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
	"travelmcp/internal/support/namesim"
)

func parseLatLon(s string) (float64, float64, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ожидалось lat,lon")
	}
	la, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, err
	}
	lo, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, err
	}
	return la, lo, nil
}

type LegacyMatchStore interface {
	SkeletonStore
	ListLegacyTerminals(ctx context.Context) ([]store.LegacyTerminalRow, error)
	ListSkeletonTerminals(ctx context.Context) ([]store.SkeletonTerminalRow, error)
	ListAttributeStates(ctx context.Context, entityType string, entityID int64) ([]store.AttributeStateRow, error)
}

type LegacyMatchSummary struct {
	In      int
	Matched int
	Updated int
	Review  int
}

func CheckLegacyBalance(s LegacyMatchSummary) error {
	if s.In != s.Matched+s.Review {
		return fmt.Errorf("sync phase0: несходимость строк: in=%d matched=%d review=%d", s.In, s.Matched, s.Review)
	}
	return nil
}

func BeginPhase0Run(ctx context.Context, st SkeletonStore, planID, inputSHA, tag string) (int64, error) {
	return st.CreateSyncRun(ctx, store.SyncRunRow{PlanID: planID, Kind: "phase0", InputSHA: inputSHA, Tag: tag})
}

func MatchLegacyTerminals(ctx context.Context, st store.Store, runID int64, threshold float64, cfg skeleton.JoinConfig) (LegacyMatchSummary, error) {
	outer, ok := st.(LegacyMatchStore)
	if !ok {
		return LegacyMatchSummary{}, fmt.Errorf("sync phase0: стор не умеет legacy-матчинг")
	}
	cfg.Threshold = threshold
	legacies, err := outer.ListLegacyTerminals(ctx)
	if err != nil {
		return LegacyMatchSummary{}, err
	}
	skeletons, err := outer.ListSkeletonTerminals(ctx)
	if err != nil {
		return LegacyMatchSummary{}, err
	}
	skelAdapted := make([]model.AdaptedRecord, 0, len(skeletons))
	for _, s := range skeletons {
		skelAdapted = append(skelAdapted, skeletonTerminalAdapted(s))
	}
	sum := LegacyMatchSummary{In: len(legacies)}
	for _, leg := range legacies {
		if err := ctx.Err(); err != nil {
			return LegacyMatchSummary{}, err
		}
		matched, updated := false, false
		err := st.WithTx(ctx, func(tx store.Store) error {
			ts, ok := tx.(LegacyMatchStore)
			if !ok {
				return fmt.Errorf("sync phase0: транзакция не умеет legacy-матчинг")
			}
			res := skeleton.Join([]model.AdaptedRecord{legacyTerminalAdapted(leg)}, skelAdapted, cfg)
			if len(res.Canon) == 0 {
				return fmt.Errorf("sync phase0: пустой join для терминала %d", leg.ID)
			}
			j := res.Canon[0]
			if j.Enrichment != skeleton.Enriched {
				mr := MatchLegacy(j.Score, threshold)
				matched = false
				return ts.SaveReviewQueue(ctx, model.ReviewQueueEntry{
					EntityType:  "terminal",
					EntityID:    leg.ID,
					Reason:      mr.ReviewReason,
					Score:       j.Score,
					Fingerprint: legacyFingerprint(leg),
				})
			}
			matched = true
			u, err := contestLegacyFields(ctx, ts, runID, leg, j)
			if err != nil {
				return err
			}
			updated = u
			return nil
		})
		if err != nil {
			return LegacyMatchSummary{}, err
		}
		if matched {
			sum.Matched++
		} else {
			sum.Review++
		}
		if updated {
			sum.Updated++
		}
	}
	if err := CheckLegacyBalance(sum); err != nil {
		return LegacyMatchSummary{}, err
	}
	slog.Info("sync phase0: legacy-матчинг завершён", "run_id", runID, "matched", sum.Matched, "updated", sum.Updated, "review", sum.Review)
	return sum, nil
}

func contestLegacyFields(ctx context.Context, st LegacyMatchStore, runID int64, leg store.LegacyTerminalRow, j skeleton.JoinedRecord) (bool, error) {
	now := time.Now()
	syncID := &runID
	states, err := st.ListAttributeStates(ctx, "terminal", leg.ID)
	if err != nil {
		return false, err
	}
	manual := map[string]AttributeCandidate{}
	for _, a := range states {
		if a.ActorID != nil && (a.Field == "geom" || a.Field == "name_ru") {
			manual[a.Field] = AttributeCandidate{Field: a.Field, Value: a.Value, Source: a.Source, Confidence: a.Confidence, ObservedAt: now, ActorID: a.ActorID, Origin: a.Origin}
		}
	}
	geomCands := legacyGeomCandidates(leg, now)
	skelLat, skelLon := j.Record.Lat, j.Record.Lon
	if skelLat != nil && skelLon != nil {
		geomCands = append(geomCands, AttributeCandidate{Field: "geom", Value: fmt.Sprintf("%v,%v", *skelLat, *skelLon), Source: geomWinnerSource(j.Record), Confidence: j.Score, ObservedAt: now, Origin: "live"})
	}
	if m, ok := manual["geom"]; ok {
		geomCands = append(geomCands, m)
	}
	nameCands := legacyNameCandidates(leg, now)
	if j.Record.NameRu != "" {
		nameCands = append(nameCands, AttributeCandidate{Field: "name_ru", Value: j.Record.NameRu, Source: geomWinnerSource(j.Record), Confidence: j.Score, ObservedAt: now, Origin: "live"})
	}
	if m, ok := manual["name_ru"]; ok {
		nameCands = append(nameCands, m)
	}
	updated := false
	if len(geomCands) > 0 {
		w, ok := ElectField(geomCands)
		if !ok {
			return false, fmt.Errorf("sync phase0: пустой конкурс geom для %d", leg.ID)
		}
		for _, c := range geomCands {
			if c.ActorID != nil {
				continue
			}
			if err := st.UpsertAttributeState(ctx, candidateToRow("terminal", leg.ID, c, syncID)); err != nil {
				return false, err
			}
		}
		if w.ActorID == nil && w.Value != fmt.Sprintf("%v,%v", leg.Lat, leg.Lon) {
			la, lo, err := parseLatLon(w.Value)
			if err != nil {
				return false, fmt.Errorf("sync phase0: битый geom-победитель %q: %w", w.Value, err)
			}
			if err := upsertLegacyTerminal(ctx, st, leg, &la, &lo, ""); err != nil {
				return false, err
			}
			updated = true
		}
	}
	if len(nameCands) > 0 {
		w, ok := ElectField(nameCands)
		if !ok {
			return false, fmt.Errorf("sync phase0: пустой конкурс name_ru для %d", leg.ID)
		}
		for _, c := range nameCands {
			if c.ActorID != nil {
				continue
			}
			if err := st.UpsertAttributeState(ctx, candidateToRow("terminal", leg.ID, c, syncID)); err != nil {
				return false, err
			}
		}
		if w.ActorID == nil && w.Value != leg.NameRu {
			if err := upsertLegacyTerminal(ctx, st, leg, nil, nil, w.Value); err != nil {
				return false, err
			}
			updated = true
		}
	}
	if err := st.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: leg.ID, Source: geomWinnerSource(j.Record), Confidence: j.Score, ObservedAt: now, Channel: model.ChannelLocalFile}); err != nil {
		return false, err
	}
	return updated, nil
}

func upsertLegacyTerminal(ctx context.Context, st LegacyMatchStore, leg store.LegacyTerminalRow, lat, lon *float64, nameRu string) error {
	row := store.TerminalRow{ID: leg.ID, Lat: leg.Lat, Lon: leg.Lon, Tz: leg.Tz}
	if lat != nil && lon != nil {
		row.Lat, row.Lon = *lat, *lon
	}
	name := leg.NameRu
	if nameRu != "" {
		name = nameRu
	}
	names := map[string]string{"ru": name}
	if leg.NameEn != "" {
		names["en"] = leg.NameEn
	}
	_, err := st.UpsertTerminal(ctx, row, names, leg.Identifiers)
	return err
}

func legacyGeomCandidates(leg store.LegacyTerminalRow, now time.Time) []AttributeCandidate {
	if leg.Lat == 0 && leg.Lon == 0 {
		return nil
	}
	src, conf, at := legacyVote(leg.Votes, now)
	return []AttributeCandidate{{Field: "geom", Value: fmt.Sprintf("%v,%v", leg.Lat, leg.Lon), Source: src, Confidence: conf, ObservedAt: at, Origin: "live"}}
}

func legacyNameCandidates(leg store.LegacyTerminalRow, now time.Time) []AttributeCandidate {
	if leg.NameRu == "" {
		return nil
	}
	src, conf, at := legacyVote(leg.Votes, now)
	return []AttributeCandidate{{Field: "name_ru", Value: leg.NameRu, Source: src, Confidence: conf, ObservedAt: at, Origin: "live"}}
}

func legacyVote(votes []store.ProvenanceVote, now time.Time) (string, float64, time.Time) {
	if len(votes) == 0 {
		return "mintrans", 0.5, now
	}
	best := votes[0]
	for _, v := range votes[1:] {
		if v.Confidence > best.Confidence {
			best = v
		}
	}
	at := now
	if best.ObservedAt > 0 {
		at = time.Unix(best.ObservedAt, 0)
	}
	return best.Source, best.Confidence, at
}

func candidateToRow(entityType string, entityID int64, c AttributeCandidate, syncID *int64) store.AttributeStateRow {
	return store.AttributeStateRow{EntityType: entityType, EntityID: entityID, Field: c.Field, Value: c.Value, Source: c.Source, Confidence: c.Confidence, Origin: c.Origin, ActorID: c.ActorID, SyncRunID: syncID}
}

func legacyTerminalAdapted(leg store.LegacyTerminalRow) model.AdaptedRecord {
	r := model.AdaptedRecord{Kind: model.AdaptedTerminal, NameRu: leg.NameRu, NameEn: leg.NameEn, Source: "mintrans", Identifiers: toAdaptedIDs(leg.Identifiers)}
	if leg.Lat != 0 || leg.Lon != 0 {
		la, lo := leg.Lat, leg.Lon
		r.Lat, r.Lon = &la, &lo
	}
	if s := namesim.ExtractSettlement(leg.NameRu); s != "" {
		r.Extra = map[string]string{"settlement": s}
	}
	return r
}

func skeletonTerminalAdapted(s store.SkeletonTerminalRow) model.AdaptedRecord {
	la, lo := s.Lat, s.Lon
	r := model.AdaptedRecord{Kind: model.AdaptedTerminal, NameRu: s.NameRu, Lat: &la, Lon: &lo, Source: "osm", Identifiers: toAdaptedIDs(s.Identifiers)}
	if s.Settlement != "" {
		r.Extra = map[string]string{"settlement": s.Settlement}
	}
	return r
}

func toAdaptedIDs(ids []model.AdaptedIdentifier) []model.AdaptedIdentifier {
	return append([]model.AdaptedIdentifier{}, ids...)
}

func legacyFingerprint(leg store.LegacyTerminalRow) string {
	if len(leg.Identifiers) > 0 {
		id := leg.Identifiers[0]
		return id.System + ":" + id.Code
	}
	return "mintrans:" + leg.NameRu
}

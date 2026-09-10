package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/store"
)

// AttributeHygieneSweeper — узкая возможность стора для freshness-sweep
// Фазы 5 (§2 план, §3.1/§8): finalize/GC attribute_state, аудит possible_merge,
// recompute transport_types, stale-счётчики. Реализация — postgres
// (internal/store/postgres/attribute_hygiene.go). Интерфейс объявлен
// потребителем (SOLID); memory-зеркала не нужны — механизмы админские,
// тестируются живой scratch-БД и SQL-проверками.
type AttributeHygieneSweeper interface {
	FinalizeAndGCAttributeState(ctx context.Context, retention time.Duration) (store.AttributeSweepResult, error)
	StaleAttributesCounters(ctx context.Context, olderThan time.Duration) (map[string]int, error)
	AuditPossibleMerges(ctx context.Context, radiusM float64, limit int) ([]store.MergeAuditCandidate, error)
	RecomputeTransportTypes(ctx context.Context) (store.TransportTypesRecomputeResult, error)
}

// store.MergeAuditCandidate — терминал-кандидат аудита possible_merge: мульти-тип
// хаб либо терминал рядом с другим терминалом пересекающегося типа.
// store.TransportTypesRecomputeResult — сверка материализованного агрегата
// HygieneSweepReport — сводка одного freshness-sweep прогона (KPI §8,
// пишется в sync_runs.summary вызывателем при необходимости).
type HygieneSweepReport struct {
	Attribute AttributeSweepPart `json:"attribute"`
	Merges    MergeSweepPart     `json:"merges"`
	Types     TypeSweepPart      `json:"transport_types"`
}

type AttributeSweepPart struct {
	FinalizedToHistory int            `json:"finalized_to_history"`
	GCSupersededSeed   int            `json:"gc_superseded_seed"`
	RemainingLive      int            `json:"remaining_live"`
	Stale30d           map[string]int `json:"stale_30d"`
}

type MergeSweepPart struct {
	CandidatesFound int `json:"candidates_found"`
	SentToReview    int `json:"sent_to_review"`
}

type TypeSweepPart struct {
	Checked int            `json:"checked"`
	Updated int            `json:"updated"`
	ByDelta map[string]int `json:"by_delta"`
}

// RunHygieneSweep — один прогон freshness-sweep Фазы 5:
//  1. finalize/GC attribute_state (retention из конфига; ручные правки не тронуты);
//  2. stale-счётчики (30д — окно v_stale_attributes-дашборда);
//  3. аудит possible_merge → review_queue{possible_merge} (fingerprint=merge:a:b,
//     идемпотентно: пере-детекция бампает count, дублей нет);
//  4. recompute transport_types (сверка агрегата, пустой факт не обнуляет).
//
// Sweep не атомарен по шагам: каждый — независимая транзакция/идемпотентная
// операция, сбой одного не откатывает остальные (частичность видна в сводке).
func RunHygieneSweep(ctx context.Context, sw AttributeHygieneSweeper, rq ReviewQueueWriter, retention time.Duration, logger *slog.Logger) (HygieneSweepReport, error) {
	rep := HygieneSweepReport{}
	rep.Attribute.Stale30d = map[string]int{}
	rep.Types.ByDelta = map[string]int{}

	if retention <= 0 {
		retention = 90 * 24 * time.Hour
	}

	// 1. finalize/GC attribute_state
	fin, err := sw.FinalizeAndGCAttributeState(ctx, retention)
	if err != nil {
		return rep, fmt.Errorf("hygiene sweep: finalize/gc attribute_state: %w", err)
	}
	rep.Attribute.FinalizedToHistory = fin.FinalizedToHistory
	rep.Attribute.GCSupersededSeed = fin.GCSupersededSeed
	rep.Attribute.RemainingLive = fin.RemainingLive

	// 2. stale-счётчики (30д)
	stale, err := sw.StaleAttributesCounters(ctx, 30*24*time.Hour)
	if err != nil {
		return rep, fmt.Errorf("hygiene sweep: stale counters: %w", err)
	}
	rep.Attribute.Stale30d = stale

	// 3. аудит possible_merge: кандидаты → review
	cands, err := sw.AuditPossibleMerges(ctx, 300, 500)
	if err != nil {
		return rep, fmt.Errorf("hygiene sweep: possible_merge audit: %w", err)
	}
	rep.Merges.CandidatesFound = len(cands)
	for _, c := range cands {
		a, b := c.TerminalID, c.NeighborID
		if b < a {
			a, b = b, a
		}
		entry := model.ReviewQueueEntry{
			EntityType:  "terminal",
			EntityID:    a,
			Reason:      "possible_merge",
			Fingerprint: fmt.Sprintf("merge:%d:%d", a, b),
		}
		if err := rq.SaveReviewQueue(ctx, entry); err != nil {
			return rep, fmt.Errorf("hygiene sweep: review write merge %d+%d: %w", a, b, err)
		}
		rep.Merges.SentToReview++
	}

	// 4. recompute transport_types
	tt, err := sw.RecomputeTransportTypes(ctx)
	if err != nil {
		return rep, fmt.Errorf("hygiene sweep: transport_types recompute: %w", err)
	}
	rep.Types.Checked = tt.Checked
	rep.Types.Updated = tt.Updated
	rep.Types.ByDelta = tt.ByDelta

	if logger != nil {
		logger.Info("hygiene sweep done",
			"finalized", rep.Attribute.FinalizedToHistory,
			"gc_seed", rep.Attribute.GCSupersededSeed,
			"stale_30d_keys", len(rep.Attribute.Stale30d),
			"merge_candidates", rep.Merges.CandidatesFound,
			"types_checked", rep.Types.Checked,
			"types_updated", rep.Types.Updated)
	}
	return rep, nil
}

// ReviewQueueWriter — writer review_queue для sweep (PostgresStore и pgTxStore
// удовлетворяют; синоним существующей способности TripsStore без его ширины).
type ReviewQueueWriter interface {
	SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error
}

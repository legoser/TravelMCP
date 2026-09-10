package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/store"
)

type fakeSweeper struct {
	finalize store.AttributeSweepResult
	merges   []store.MergeAuditCandidate
	types    store.TransportTypesRecomputeResult
	stale    map[string]int

	finalizeErr bool
	mergesErr   bool
}

func (f *fakeSweeper) FinalizeAndGCAttributeState(ctx context.Context, retention time.Duration) (store.AttributeSweepResult, error) {
	if f.finalizeErr {
		return store.AttributeSweepResult{}, errors.New("finalize boom")
	}
	return f.finalize, nil
}

func (f *fakeSweeper) StaleAttributesCounters(ctx context.Context, olderThan time.Duration) (map[string]int, error) {
	return f.stale, nil
}

func (f *fakeSweeper) AuditPossibleMerges(ctx context.Context, radiusM float64, limit int) ([]store.MergeAuditCandidate, error) {
	if f.mergesErr {
		return nil, errors.New("merges boom")
	}
	return f.merges, nil
}

func (f *fakeSweeper) RecomputeTransportTypes(ctx context.Context) (store.TransportTypesRecomputeResult, error) {
	return f.types, nil
}

type fakeReviewQ struct {
	entries []model.ReviewQueueEntry
}

func (f *fakeReviewQ) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	f.entries = append(f.entries, e)
	return nil
}

func TestRunHygieneSweepWritesMergeReviews(t *testing.T) {
	sw := &fakeSweeper{
		finalize: store.AttributeSweepResult{FinalizedToHistory: 5, GCSupersededSeed: 2, RemainingLive: 100},
		stale:    map[string]int{"geom/osm": 12},
		merges: []store.MergeAuditCandidate{
			{TerminalID: 30, NeighborID: 10, Name: "А", NeighborName: "Б"},
			{TerminalID: 20, NeighborID: 40, Name: "В", NeighborName: "Г"},
		},
		types: store.TransportTypesRecomputeResult{Checked: 1000, Updated: 3, ByDelta: map[string]int{"trolley→bus": 3}},
	}
	rq := &fakeReviewQ{}
	rep, err := RunHygieneSweep(context.Background(), sw, rq, 90*24*time.Hour, nil)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if rep.Attribute.FinalizedToHistory != 5 || rep.Attribute.GCSupersededSeed != 2 || rep.Attribute.RemainingLive != 100 {
		t.Fatalf("attribute part: %+v", rep.Attribute)
	}
	if rep.Merges.CandidatesFound != 2 || rep.Merges.SentToReview != 2 {
		t.Fatalf("merges part: %+v", rep.Merges)
	}
	// fingerprint канонический: меньший id первым (идемпотентность ключа)
	if rq.entries[0].Fingerprint != "merge:10:30" || rq.entries[1].Fingerprint != "merge:20:40" {
		t.Fatalf("fingerprints: %+v", rq.entries)
	}
	if rq.entries[0].Reason != "possible_merge" || rq.entries[0].EntityType != "terminal" {
		t.Fatalf("entry: %+v", rq.entries[0])
	}
	if rep.Types.Updated != 3 || rep.Types.ByDelta["trolley→bus"] != 3 {
		t.Fatalf("types part: %+v", rep.Types)
	}
	if rep.Attribute.Stale30d["geom/osm"] != 12 {
		t.Fatalf("stale part: %+v", rep.Attribute.Stale30d)
	}
}

func TestRunHygieneSweepDefaultRetention(t *testing.T) {
	called := 0
	sw := &retentionSpy{sweeper: &fakeSweeper{}, called: &called}
	if _, err := RunHygieneSweep(context.Background(), sw, &fakeReviewQ{}, 0, nil); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if called == 0 || sw.got != 90*24*time.Hour {
		t.Fatalf("retention default: got %s, want 90d", sw.got)
	}
}

type retentionSpy struct {
	sweeper *fakeSweeper
	called  *int
	got     time.Duration
}

func (r *retentionSpy) FinalizeAndGCAttributeState(ctx context.Context, retention time.Duration) (store.AttributeSweepResult, error) {
	*r.called++
	r.got = retention
	return r.sweeper.FinalizeAndGCAttributeState(ctx, retention)
}
func (r *retentionSpy) StaleAttributesCounters(ctx context.Context, olderThan time.Duration) (map[string]int, error) {
	return r.sweeper.StaleAttributesCounters(ctx, olderThan)
}
func (r *retentionSpy) AuditPossibleMerges(ctx context.Context, radiusM float64, limit int) ([]store.MergeAuditCandidate, error) {
	return r.sweeper.AuditPossibleMerges(ctx, radiusM, limit)
}
func (r *retentionSpy) RecomputeTransportTypes(ctx context.Context) (store.TransportTypesRecomputeResult, error) {
	return r.sweeper.RecomputeTransportTypes(ctx)
}

func TestRunHygieneSweepFailLoud(t *testing.T) {
	// сбой finalize останавливает sweep с loud-ошибкой — тихая частичность
	// допустима только между шагами, не внутри
	sw := &fakeSweeper{finalizeErr: true}
	if _, err := RunHygieneSweep(context.Background(), sw, &fakeReviewQ{}, 90*24*time.Hour, nil); err == nil {
		t.Fatal("finalize fail должен прервать sweep")
	}
	// сбой merge-аудита: attribute-часть уже применена, ошибка возвращена
	sw2 := &fakeSweeper{mergesErr: true, finalize: store.AttributeSweepResult{FinalizedToHistory: 1}}
	if _, err := RunHygieneSweep(context.Background(), sw2, &fakeReviewQ{}, 90*24*time.Hour, nil); err == nil {
		t.Fatal("merges fail должен вернуть ошибку")
	}
}

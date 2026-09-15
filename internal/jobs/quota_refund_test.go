package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

func quotaUsed(t *testing.T, ms *memory.MemoryStore, provider string) int {
	t.Helper()
	q, found := ms.GetQuota(context.Background(), provider, time.Now())
	if !found {
		return 0
	}
	return q.Used
}

func TestQuotaRefundOnFailure(t *testing.T) {
	ctx := context.Background()
	ms := memory.NewMemoryStore()
	w := NewWorker(ms, quietLogger())
	fail := true
	w.Register(string(JobSyncRail), func(ctx context.Context, job store.JobRow) error {
		if fail {
			return errors.New("external call failed")
		}
		return nil
	})
	for _, p := range []string{"yandex", "nominatim", "motis"} {
		if err := ms.SetQuotaLimit(ctx, p, 100); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ms.EnqueueJob(ctx, store.JobRow{Type: string(JobSyncRail), Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); err == nil {
		t.Fatal("expected handler error")
	}
	// Consumed reservations must be refunded: budget counts successes only.
	for _, p := range []string{"yandex", "nominatim", "motis"} {
		if got := quotaUsed(t, ms, p); got != 0 {
			t.Fatalf("provider %s used=%d after failure, want 0 (refunded)", p, got)
		}
	}

	fail = false
	if _, err := ms.EnqueueJob(ctx, store.JobRow{Type: string(JobSyncRail), Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := quotaUsed(t, ms, "yandex"); got != 1 {
		t.Fatalf("yandex used=%d after success, want 1", got)
	}
}

func TestQuotaExhaustedSkipsHandler(t *testing.T) {
	ctx := context.Background()
	ms := memory.NewMemoryStore()
	w := NewWorker(ms, quietLogger())
	runs := 0
	w.Register(string(JobSyncRail), func(ctx context.Context, job store.JobRow) error {
		runs++
		return nil
	})
	for _, p := range []string{"yandex", "nominatim", "motis"} {
		if err := ms.SetQuotaLimit(ctx, p, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		if _, err := ms.EnqueueJob(ctx, store.JobRow{Type: string(JobSyncRail), Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
		_ = w.RunOnce(ctx)
	}
	// Three providers x limit 1 = three successes; the fourth job finds
	// every quota exhausted and never runs the handler.
	if runs != 3 {
		t.Fatalf("handler runs=%d, want 3 (fourth job must not run)", runs)
	}
	if got := quotaUsed(t, ms, "yandex") + quotaUsed(t, ms, "nominatim") + quotaUsed(t, ms, "motis"); got != 3 {
		t.Fatalf("total used=%d, want 3", got)
	}
}

func TestRefundFloor(t *testing.T) {
	ctx := context.Background()
	ms := memory.NewMemoryStore()
	if err := ms.RefundQuota(ctx, "ghost"); err != nil {
		t.Fatalf("refund without row: %v", err)
	}
	if got := quotaUsed(t, ms, "ghost"); got != 0 {
		t.Fatalf("used=%d, want 0 floor", got)
	}
}

package jobs

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
	syncpkg "travelmcp/internal/sync"
)

// TestWorkerOfflineCacheEmptyGoesDead — пустой offline-сбор: сразу dead,
// без ретраев (кэш сам не появится) и без двойного инкремента attempts
// (одна попытка = один claim).
func TestWorkerOfflineCacheEmptyGoesDead(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	w := NewWorker(ms, quietLogger())
	w.Register("sync_collect_region", func(ctx context.Context, job store.JobRow) error {
		return &syncpkg.ErrOfflineCacheEmpty{TerminalID: 7, Code: "s1", Region: "R", Date: "2026-09-17"}
	})
	id, err := ms.EnqueueJob(ctx, store.JobRow{Type: "sync_collect_region", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	j, _ := ms.ListJobs(ctx, 10)
	for _, r := range j {
		if r.ID != id {
			continue
		}
		if r.State != "dead" {
			t.Fatalf("state=%q, want dead (offline-пусто не ретраится)", r.State)
		}
		if r.Attempts != 1 {
			t.Fatalf("attempts=%d, want 1 (инкремент только в claim)", r.Attempts)
		}
	}
}

// TestWorkerQuotaBlockedRetriesAtReset — исчерпанная квота: retry с
// next_run на время сброса квоты (reset_at + джиттер), а не минутный
// бэкофф.
func TestWorkerQuotaBlockedRetriesAtReset(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	if err := ms.SetQuotaLimit(ctx, "yandex_rasp", 500); err != nil {
		t.Fatal(err)
	}
	q, ok := ms.GetQuota(ctx, "yandex_rasp", time.Now().UTC())
	if !ok || q.ResetAt == nil || *q.ResetAt == "" {
		t.Fatalf("quota reset_at обязан быть задан: %+v %v", q, ok)
	}
	reset, err := time.Parse("2006-01-02 15:04:05-07:00", *q.ResetAt)
	if err != nil {
		t.Fatalf("reset_at parse: %v", err)
	}
	w := NewWorker(ms, quietLogger())
	w.Register("sync_collect_region", func(ctx context.Context, job store.JobRow) error {
		return &syncpkg.ErrQuotaBlocked{Provider: "yandex_rasp"}
	})
	id, err := ms.EnqueueJob(ctx, store.JobRow{Type: "sync_collect_region", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	j, _ := ms.ListJobs(ctx, 10)
	for _, r := range j {
		if r.ID != id {
			continue
		}
		if r.State != "retry" {
			t.Fatalf("state=%q, want retry", r.State)
		}
		next, err := time.Parse(time.RFC3339, r.NextRun)
		if err != nil {
			t.Fatalf("next_run parse: %v", err)
		}
		want := reset.Add(2 * time.Minute)
		if next.Before(want.Add(-time.Minute)) || next.After(want.Add(time.Minute)) {
			t.Fatalf("next_run=%v, want около %v (сброс квоты + джиттер)", next, want)
		}
		if next.Before(time.Now().Add(time.Hour)) {
			t.Fatalf("next_run=%v: минутный бэкофф для квоты недопустим", next)
		}
	}
}

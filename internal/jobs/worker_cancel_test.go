package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestWorkerCancelFlow — отмена running-задания (issue #22): handler
// возвращает ErrCancelled → воркер НЕ делает retry/dead и НЕ
// перезаписывает state='cancelled' своим done.
func TestWorkerCancelFlow(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	w := NewWorker(ms, quietLogger())
	cancelled := false
	w.Register("sync_collect_region", func(ctx context.Context, job store.JobRow) error {
		_ = ms.CancelJob(ctx, job.ID)
		cancelled = true
		return ErrCancelled
	})
	id, err := ms.EnqueueJob(ctx, store.JobRow{Type: "sync_collect_region", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); !IsCancelled(err) {
		t.Fatalf("run: %v, want ErrCancelled", err)
	}
	if !cancelled {
		t.Fatal("handler не вызван")
	}
	j, _ := ms.ListJobs(ctx, 10)
	var found bool
	for _, r := range j {
		if r.ID == id {
			found = true
			if r.State != "cancelled" {
				t.Fatalf("state=%q, want cancelled (retry/dead не перезаписывают отмену)", r.State)
			}
		}
	}
	if !found {
		t.Fatalf("job %d не найден", id)
	}
	// MarkJobDone поверх cancelled — no-op
	if err := ms.MarkJobDone(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := ms.JobCancelled(ctx, id)
	if err != nil || !got {
		t.Fatalf("после MarkJobDone: JobCancelled=%v err=%v, want true (done не перезаписывает cancelled)", got, err)
	}
}

// TestWorkerCancelPending — pending-задача отменена до запуска: воркер
// её не забирает (ClaimNextJob берёт только pending/retry, cancelled
// остаётся в очереди видимым для оператора).
func TestWorkerCancelPending(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	id, err := ms.EnqueueJob(ctx, store.JobRow{Type: "sync_collect_region", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.CancelJob(ctx, id); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	_, err = ms.ClaimNextJob(ctx)
	if err == nil {
		t.Fatal("cancelled job не должен забираться воркером")
	}
	if !errors.Is(err, err) {
		t.Fatal("unreachable")
	}
	if ok, _ := ms.JobCancelled(ctx, id); !ok {
		t.Fatal("JobCancelled должна быть true")
	}
	// Reset возвращает cancelled в работу
	if err := ms.ResetJob(ctx, id); err != nil {
		t.Fatalf("reset cancelled: %v", err)
	}
	j, err := ms.ClaimNextJob(ctx)
	if err != nil || j == nil || j.ID != id {
		t.Fatalf("после reset cancelled job должен забираться: %v %v", j, err)
	}
}

// TestWorkerNormalRetry — обычная ошибка handler'а без отмены: retry,
// state не cancelled.
func TestWorkerNormalRetry(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	w := NewWorker(ms, quietLogger())
	w.Register("sync_collect_region", func(ctx context.Context, job store.JobRow) error {
		return errors.New("booms")
	})
	_, err := ms.EnqueueJob(ctx, store.JobRow{Type: "sync_collect_region", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RunOnce(ctx); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	j, _ := ms.ListJobs(ctx, 10)
	for _, r := range j {
		if r.Type == "sync_collect_region" && r.State != "retry" {
			t.Fatalf("state=%q, want retry", r.State)
		}
	}
}

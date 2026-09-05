package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"travelmcp/internal/store"
)

type Handler func(ctx context.Context, job store.JobRow) error

type Worker struct {
	store    store.Store
	handlers map[string]Handler
	logger   *slog.Logger
}

func NewWorker(st store.Store, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: st, handlers: map[string]Handler{}, logger: logger}
}

func (w *Worker) Register(jobType string, h Handler) {
	w.handlers[jobType] = h
}

func (w *Worker) RunOnce(ctx context.Context) error {
	job, err := w.store.ClaimNextJob(ctx)
	if err != nil {
		return err
	}
	w.logger.Info("job claimed", "id", job.ID, "type", job.Type, "attempts", job.Attempts)
	h, ok := w.handlers[job.Type]
	if !ok {
		w.logger.Warn("no handler for job type", "type", job.Type)
		_ = w.store.MarkJobDead(ctx, job.ID, "no handler")
		return fmt.Errorf("no handler for %s", job.Type)
	}
	if err := w.withQuota(ctx, job, h); err != nil {
		if isRateLimited(err) {
			w.logger.Warn("job rate limited, retry", "id", job.ID, "err", err)
			_ = w.store.MarkJobRetry(ctx, job.ID, err.Error())
			return err
		}
		if job.Attempts >= 5 {
			_ = w.store.MarkJobDead(ctx, job.ID, err.Error())
		} else {
			_ = w.store.MarkJobRetry(ctx, job.ID, err.Error())
		}
		return err
	}
	_ = w.store.MarkJobDone(ctx, job.ID)
	w.logger.Info("job done", "id", job.ID)
	return nil
}

func (w *Worker) withQuota(ctx context.Context, job *store.JobRow, h Handler) error {
	providers := w.providersForJob(job)
	if len(providers) == 0 {
		return h(ctx, *job)
	}
	var lastErr error
	for _, p := range providers {
		limit := 1000
		ok, _, _ := w.store.TryConsumeQuota(ctx, p, limit)
		if !ok {
			lastErr = fmt.Errorf("429 quota exhausted for %s", p)
			continue
		}
		_ = w.store.RecordApiCall(ctx, p, string(job.Type), 1)
		if err := h(ctx, *job); err != nil {
			lastErr = err
			if isRateLimited(err) {
				continue
			}
			return err
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("429 all quotas exhausted")
}

func (w *Worker) providersForJob(job *store.JobRow) []string {
	switch job.Type {
	case string(JobSyncMintrans):
		return []string{"mintrans", "yandex", "nominatim"}
	case string(JobSyncRail):
		return []string{"yandex", "nominatim", "motis"}
	case string(JobImportGTFS):
		return []string{"gtfs"}
	default:
		var payload map[string]any
		if err := json.Unmarshal([]byte(job.Payload), &payload); err == nil {
			if p, ok := payload["provider"].(string); ok && p != "" {
				return []string{p}
			}
		}
		return nil
	}
}

func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "429") || contains(s, "rate") || contains(s, "quota")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.RunOnce(ctx)
		}
	}
}

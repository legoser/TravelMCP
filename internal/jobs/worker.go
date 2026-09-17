package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"travelmcp/internal/store"
	syncpkg "travelmcp/internal/sync"
)

type Handler func(ctx context.Context, job store.JobRow) error

// Worker-local values (not config): maxAttempts — attempts before
// dead-letter (transient errors survive retries with Backoff);
// defaultPollInterval — queue poll when Run is called without interval.
const (
	maxAttempts         = 5
	defaultPollInterval = 5 * time.Second
)

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
		if IsCancelled(err) {
			w.logger.Warn("job cancelled by operator", "id", job.ID, "type", job.Type)
			return err
		}
		var offlineErr *syncpkg.ErrOfflineCacheEmpty
		if errors.As(err, &offlineErr) {
			w.logger.Warn("job dead: offline cache empty, retry pointless", "id", job.ID, "type", job.Type, "err", err)
			_ = w.store.MarkJobDead(ctx, job.ID, err.Error())
			return err
		}
		var quotaErr *syncpkg.ErrQuotaBlocked
		if errors.As(err, &quotaErr) {
			nextRun := w.quotaResetAt(ctx, quotaErr.Provider)
			w.logger.Warn("job quota blocked, retry after reset", "id", job.ID, "type", job.Type, "provider", quotaErr.Provider, "next_run", nextRun)
			_ = w.store.MarkJobRetryAt(ctx, job.ID, err.Error(), nextRun)
			return err
		}
		if isRateLimited(err) {
			w.logger.Warn("job rate limited, retry", "id", job.ID, "err", err)
			_ = w.store.MarkJobRetry(ctx, job.ID, err.Error())
			return err
		}
		if job.Attempts >= maxAttempts {
			w.logger.Error("job dead", "id", job.ID, "type", job.Type, "attempts", job.Attempts, "err", err)
			_ = w.store.MarkJobDead(ctx, job.ID, err.Error())
		} else {
			w.logger.Warn("job failed, retry", "id", job.ID, "type", job.Type, "attempts", job.Attempts, "err", err)
			_ = w.store.MarkJobRetry(ctx, job.ID, err.Error())
		}
		return err
	}
	if w.jobState(ctx, job.ID) == "cancelled" {
		w.logger.Warn("job finished after cancel", "id", job.ID)
		return nil
	}
	_ = w.store.MarkJobDone(ctx, job.ID)
	w.logger.Info("job done", "id", job.ID)
	return nil
}

// ErrCancelled — маркер отмены: handler увидел JobCancelled и завершился.
var ErrCancelled = fmt.Errorf("job cancelled by operator")

// IsCancelled — распознавание отмены в ошибке handler'а.
func IsCancelled(err error) bool {
	return err != nil && err == ErrCancelled
}

// jobState — текущее состояние задания (для гонки done поверх cancelled).
func (w *Worker) jobState(ctx context.Context, id int64) string {
	if l, ok := w.store.(interface {
		JobCancelled(ctx context.Context, id int64) (bool, error)
	}); ok {
		if cancelled, err := l.JobCancelled(ctx, id); err == nil && cancelled {
			return "cancelled"
		}
	}
	return ""
}

// withQuota reserves budget before the handler and refunds it on failure,
// so api_quotas.used counts successful calls only (attempts stay in
// api_calls). Handlers MUST be idempotent: a rate-limited failure moves to
// the next provider with the same job, so the handler may run multiple
// times for one job.
func (w *Worker) withQuota(ctx context.Context, job *store.JobRow, h Handler) error {
	providers := w.providersForJob(job)
	if len(providers) == 0 {
		return h(ctx, *job)
	}
	day := time.Now().UTC()
	var lastErr error
	for _, p := range providers {
		limit := store.DefaultQuotaLimit
		ok, _, qerr := w.store.TryConsumeQuota(ctx, p, limit)
		if qerr != nil {
			return fmt.Errorf("проверка квоты %s: %w", p, qerr)
		}
		if !ok {
			lastErr = fmt.Errorf("429 quota exhausted for %s", p)
			continue
		}
		_ = w.store.RecordApiCall(ctx, p, string(job.Type), 1)
		if err := h(ctx, *job); err != nil {
			lastErr = err
			w.refundQuota(ctx, p, day)
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

// refundQuota — returns the quota reservation for the UTC day it was taken
// on. RefundQuota is part of store.Store, so every backend supports it.
func (w *Worker) refundQuota(ctx context.Context, provider string, day time.Time) {
	if err := w.store.RefundQuota(ctx, provider, day); err != nil {
		w.logger.Warn("quota refund failed", "provider", provider, "err", err)
	}
}

// quotaResetAt — время сброса суточной квоты провайдера (reset_at из
// api_quotas) + 2 минуты джиттера, чтобы не стартовать ровно в полночь
// вместе с остальной очередью. Нет строки квоты — fallback +12 часов.
func (w *Worker) quotaResetAt(ctx context.Context, provider string) time.Time {
	if q, ok := w.store.GetQuota(ctx, provider, time.Now().UTC()); ok && q.ResetAt != nil && *q.ResetAt != "" {
		for _, layout := range []string{"2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05-07", time.RFC3339} {
			if ts, err := time.Parse(layout, *q.ResetAt); err == nil {
				return ts.Add(2 * time.Minute)
			}
		}
	}
	return time.Now().Add(12 * time.Hour)
}

func (w *Worker) providersForJob(job *store.JobRow) []string {
	switch job.Type {
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
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") ||
		strings.Contains(s, "quota exhausted") ||
		strings.Contains(s, "quota_exhausted") ||
		strings.Contains(s, "quota exceeded") ||
		strings.Contains(s, "quota limit") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "rate_limit") ||
		strings.Contains(s, "ratelimit") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "try again")
}

func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = defaultPollInterval
	}
	// Однопроцессный воркер: running-задачи, оставшиеся от прошлого
	// процесса (краш/рестарт), осиротели — вернуть в очередь.
	if rec, ok := w.store.(interface {
		RecoverStuckJobs(ctx context.Context) (int, error)
	}); ok {
		if n, err := rec.RecoverStuckJobs(ctx); err == nil && n > 0 {
			w.logger.Warn("recovered orphaned running jobs", "count", n)
		}
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

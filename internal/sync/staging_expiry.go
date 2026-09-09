package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// StagingExpirer — узкая возможность стора: пометка протухших staging-строк
// dead-letter'ом (state='expired') и чтение возраста. Реализации: postgres,
// memory (тесты). Данные не удаляются — операторский разбор (§5.3).
type StagingExpirer interface {
	// ExpireStagingTripsOlderThan помечает state='expired' строки,
	// чей last_attempt_at старше cutoff (только незавершённые состояния).
	// Возвращает число помеченных.
	ExpireStagingTripsOlderThan(ctx context.Context, cutoff time.Time) (int, error)
	// CountStagingByState — счётчики по state (KPI-алерт: застрявшие старше
	// порога — alert, живое наблюдение бампает last_attempt_at и не даёт
	// строке протухнуть).
	CountStagingByState(ctx context.Context) (map[string]int, error)
}

// StagingExpiryResult — итог прогонa expiry-механизма.
type StagingExpiryResult struct {
	Expired int            // помечено dead-letter
	ByState map[string]int // счётчики после прогона
	Alert   bool           // застрявшие старше порога есть (KPI §8)
}

// ExpireStaleStagingTrips — реализация §5.3 «протухшие staging_trips —
// dead-letter + alert»: строки в незавершённых состояниях (incomplete_trip,
// awaiting_times, needs_review, skeleton_gap), не наблюдавшиеся дольше
// порога, получают state='expired'. Источник времён (awaiting_times) и
// карантин сомнительных (needs_review) не исчезают — переходят в разряд
// «ждут решения оператора/источника», отличный от «висят непонятно зачем».
//
// Повторный прогон идемпотентен: expired не входит в незавершённые состояния,
// повторная выборка их не видит.
func ExpireStaleStagingTrips(ctx context.Context, ex StagingExpirer, olderThan time.Duration, logger *slog.Logger) (StagingExpiryResult, error) {
	res := StagingExpiryResult{ByState: map[string]int{}}
	if olderThan <= 0 {
		return res, fmt.Errorf("staging expiry: порог должен быть положительным (got %s)", olderThan)
	}
	cutoff := time.Now().Add(-olderThan)
	expired, err := ex.ExpireStagingTripsOlderThan(ctx, cutoff)
	if err != nil {
		return res, err
	}
	res.Expired = expired
	byState, err := ex.CountStagingByState(ctx)
	if err != nil {
		return res, err
	}
	res.ByState = byState
	// KPI-алерт (§8): сами expired-строки и есть очередь мёртвых писем —
	// их присутствие сигнал оператору; отдельного флага не требуется,
	// счётчик читается дашбордом.
	if expired > 0 && logger != nil {
		logger.Warn("staging expiry: dead-letter",
			"expired", expired, "older_than", olderThan.String(), "by_state", fmt.Sprint(byState))
	} else if logger != nil {
		logger.Info("staging expiry: nothing to expire", "cutoff", cutoff.Format(time.RFC3339), "by_state", fmt.Sprint(byState))
	}
	return res, nil
}

// HandleCleanupJob — обработчик jobs{type=cleanup} (§5.3): expiry staging.
// payload {"staging_expiry_days": N} переопределяет дефолт из конфига.
func HandleCleanupJob(ctx context.Context, db interface {
	StagingExpirer
}, stagingExpiryDays int, logger *slog.Logger) error {
	older := time.Duration(stagingExpiryDays) * 24 * time.Hour
	if older <= 0 {
		older = 14 * 24 * time.Hour
	}
	_, err := ExpireStaleStagingTrips(ctx, db, older, logger)
	return err
}

package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type JobType string

const (
	JobImportGTFS      JobType = "import_gtfs"
	JobSyncRail        JobType = "sync_rail"
	JobNotify          JobType = "notify"
	JobCleanup         JobType = "cleanup"
	JobSyncStations    JobType = "sync_stations"
	JobSyncRefresh     JobType = "sync_refresh"
	JobSyncTerminals   JobType = "sync_terminals_chunk"
	JobSyncTripsAttach JobType = "sync_trips_attach"
)

type JobState string

const (
	StatePending JobState = "pending"
	StateRunning JobState = "running"
	StateRetry   JobState = "retry"
	StateDone    JobState = "done"
	StateDead    JobState = "dead"
)

type Job struct {
	ID        int64           `json:"id"`
	Type      JobType         `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Region    string          `json:"region,omitempty"`
	State     JobState        `json:"state"`
	Attempts  int             `json:"attempts"`
	NextRun   time.Time       `json:"next_run"`
	LastError string          `json:"last_error,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type Store interface {
	Enqueue(ctx context.Context, job Job) (int64, error)
	ClaimNext(ctx context.Context) (*Job, error)
	MarkDone(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, errMsg string) error
	MarkDead(ctx context.Context, id int64, errMsg string) error
	ListPending(ctx context.Context, limit int) ([]Job, error)
}

type Manager struct {
	db *sql.DB
}

func NewManager(db *sql.DB) *Manager { return &Manager{db: db} }

func (m *Manager) Enqueue(ctx context.Context, job Job) (int64, error) {
	if m.db == nil {
		return 0, sql.ErrConnDone
	}
	var id int64
	err := m.db.QueryRowContext(ctx,
		`INSERT INTO jobs(type, payload, region, state, next_run) VALUES($1,$2,$3,'pending', now()) ON CONFLICT DO NOTHING RETURNING id`,
		string(job.Type), job.Payload, job.Region).Scan(&id)
	return id, err
}

func (m *Manager) ClaimNext(ctx context.Context) (*Job, error) {
	if m.db == nil {
		return nil, sql.ErrConnDone
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx,
		`SELECT id, type, payload, region, state, attempts, next_run, coalesce(last_error,''), created_at
		 FROM jobs WHERE state IN ('pending','retry') AND next_run <= now()
		 ORDER BY next_run LIMIT 1 FOR UPDATE SKIP LOCKED`)
	var j Job
	var lastErr string
	if err := row.Scan(&j.ID, &j.Type, &j.Payload, &j.Region, &j.State, &j.Attempts, &j.NextRun, &lastErr, &j.CreatedAt); err != nil {
		return nil, err
	}
	j.LastError = lastErr
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='running', attempts=attempts+1, updated_at=now() WHERE id=$1`, j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	j.State = StateRunning
	return &j, nil
}

func Backoff(attempt int) time.Duration {
	// maxBackoff — потолок экспоненциальной паузы ретраев (линейный рост
	// 2мин×attempt упирается в 30 минут).
	const maxBackoff = 30 * time.Minute
	base := time.Duration(attempt*2) * time.Minute
	if base > maxBackoff {
		base = maxBackoff
	}
	return base
}

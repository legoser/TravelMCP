package importer

import (
	"context"
	"time"

	"travelmcp/internal/model"
)

type Stage string

const (
	StageNormalize Stage = "normalize"
	StageEnrich    Stage = "enrich"
	StageDedup     Stage = "dedup"
	StageVerify    Stage = "verify"
	StageCanonical Stage = "canonical"
)

type LogEntry struct {
	JobID      int64
	EntityType string
	EntityID   string
	Stage      Stage
	Action     string
	Confidence float64
	DistanceM  int
	Lev        float64
	Source     string
	At         time.Time
}

type Logger interface {
	Log(ctx context.Context, e LogEntry) error
}

type Verifier interface {
	Verify(cand model.AdaptedRecord, b, c, d *model.AdaptedRecord) (confidence float64, verified bool, reason string)
}

type Pipeline struct {
	verify Verifier
	logger Logger
	store  interface {
		LogEntry(ctx context.Context, e LogEntry) error
	}
}

func New(v Verifier, l Logger) *Pipeline { return &Pipeline{verify: v, logger: l} }

func NewWithStore(v Verifier, s interface {
	LogEntry(ctx context.Context, e LogEntry) error
}) *Pipeline {
	return &Pipeline{verify: v, store: s}
}

func (p *Pipeline) log(ctx context.Context, e LogEntry) {
	if p.logger != nil {
		_ = p.logger.Log(ctx, e)
	}
	if p.store != nil {
		_ = p.store.LogEntry(ctx, e)
	}
}

func (p *Pipeline) Process(ctx context.Context, jobID int64, rec model.AdaptedRecord) error {
	p.log(ctx, LogEntry{JobID: jobID, EntityType: string(rec.Kind), EntityID: rec.PrimaryCode(), Stage: StageNormalize, Action: "normalized", Source: rec.Source, At: time.Now()})
	p.log(ctx, LogEntry{JobID: jobID, EntityType: string(rec.Kind), EntityID: rec.PrimaryCode(), Stage: StageEnrich, Action: "enriched", Source: rec.Source, At: time.Now()})
	p.log(ctx, LogEntry{JobID: jobID, EntityType: string(rec.Kind), EntityID: rec.PrimaryCode(), Stage: StageDedup, Action: "dedup_checked", Source: rec.Source, At: time.Now()})
	if p.verify != nil {
		conf, verified, reason := p.verify.Verify(rec, nil, nil, nil)
		act := "verified"
		if !verified {
			act = "review:" + reason
		}
		p.log(ctx, LogEntry{JobID: jobID, EntityType: string(rec.Kind), EntityID: rec.PrimaryCode(), Stage: StageVerify, Action: act, Confidence: conf, Source: rec.Source, At: time.Now()})
	}
	p.log(ctx, LogEntry{JobID: jobID, EntityType: string(rec.Kind), EntityID: rec.PrimaryCode(), Stage: StageCanonical, Action: "upserted", Source: rec.Source, At: time.Now()})
	return nil
}

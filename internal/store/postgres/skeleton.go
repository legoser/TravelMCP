package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func transportTypesValue(types []string) []string {
	if types == nil {
		return []string{}
	}
	return types
}

func nullSource(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (p *PostgresStore) UpsertTerminalAlias(ctx context.Context, a store.TerminalAliasRow) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO terminal_aliases(terminal_id, alias, lang, source, observed_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(terminal_id, alias, lang) DO UPDATE SET source=EXCLUDED.source, observed_at=now()`, a.TerminalID, a.Alias, a.Lang, nullSource(a.Source))
	return err
}

func (p *PostgresStore) UpsertAttributeState(ctx context.Context, a store.AttributeStateRow) error {
	if p.pool == nil {
		return errNotImplemented
	}
	origin := a.Origin
	if origin == "" {
		origin = "live"
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO attribute_state(entity_type, entity_id, field, value, source, confidence, observed_at, actor_id, origin, sync_run_id) VALUES($1,$2,$3,to_jsonb($4::text),$5,$6,now(),$7,$8,$9) ON CONFLICT(entity_type, entity_id, field, source) DO UPDATE SET value=EXCLUDED.value, confidence=EXCLUDED.confidence, observed_at=now(), actor_id=EXCLUDED.actor_id, origin=EXCLUDED.origin, sync_run_id=EXCLUDED.sync_run_id`, a.EntityType, a.EntityID, a.Field, a.Value, a.Source, a.Confidence, a.ActorID, origin, a.SyncRunID)
	return err
}

func (p *PostgresStore) CreateSyncRun(ctx context.Context, r store.SyncRunRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO sync_runs(plan_id, kind, input_sha, tag, state, summary) VALUES($1,$2,$3,$4,'running',$5::jsonb) RETURNING id`, r.PlanID, r.Kind, r.InputSHA, r.Tag, summaryOrEmpty(r.Summary)).Scan(&id)
	return id, err
}

func (p *PostgresStore) FinishSyncRun(ctx context.Context, id int64, state, summary string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `UPDATE sync_runs SET state=$2, finished_at=now(), summary=$3::jsonb WHERE id=$1`, id, state, summaryOrEmpty(summary))
	return err
}

func summaryOrEmpty(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

type StagedTerminal struct {
	ID            int64
	Source        string
	ExternalCode  string
	NameRu        string
	Lat, Lon      float64
	HasCoords     bool
	Settlement    string
	Region        string
	TransportType string
}

func (p *PostgresStore) StageSkeletonRecords(ctx context.Context, runID int64, records []model.AdaptedRecord) error {
	if p.pool == nil {
		return errNotImplemented
	}
	for _, r := range records {
		code := r.PrimaryCode()
		if code == "" {
			code = r.NameRu
		}
		var lat, lon any
		if r.HasCoords() {
			lat, lon = *r.Lat, *r.Lon
		}
		ex := map[string]string{}
		if r.Extra != nil {
			ex = r.Extra
		}
		var geomExpr string
		var args []any
		if r.HasCoords() {
			geomExpr = `ST_SetSRID(ST_MakePoint($6,$5),4326)::geography`
			args = []any{runID, r.Source, code, r.NameRu, lat, lon, ex["settlement"], ex["region"], ex["transport_type"]}
		} else {
			geomExpr = `NULL`
			args = []any{runID, r.Source, code, r.NameRu, ex["settlement"], ex["region"], ex["transport_type"]}
		}
		q := fmt.Sprintf(`INSERT INTO staging_terminals(run_id, source, source_code, name_ru, geom, settlement, region, transport_type) VALUES($1,$2,$3,$4,%s,$5,$6,$7) ON CONFLICT(run_id, source, source_code) DO UPDATE SET name_ru=EXCLUDED.name_ru, geom=EXCLUDED.geom, settlement=EXCLUDED.settlement, region=EXCLUDED.region, transport_type=EXCLUDED.transport_type`, geomExpr)
		if _, err := p.pool.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("stage skeleton %s/%s: %w", r.Source, code, err)
		}
	}
	return nil
}

func (p *PostgresStore) NearbyStagedCandidates(ctx context.Context, runID int64, source string, lat, lon, radiusM float64, limit int) ([]StagedTerminal, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := p.pool.Query(ctx, `SELECT id, source, source_code, name_ru, ST_Y(geom::geometry), ST_X(geom::geometry), coalesce(settlement,''), coalesce(region,''), coalesce(transport_type,'') FROM staging_terminals WHERE run_id=$1 AND source<>$2 AND geom IS NOT NULL AND ST_DWithin(geom, ST_SetSRID(ST_MakePoint($4,$3),4326)::geography, $5) ORDER BY geom <-> ST_SetSRID(ST_MakePoint($4,$3),4326)::geography LIMIT $6`, runID, source, lat, lon, radiusM, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StagedTerminal
	for rows.Next() {
		var s StagedTerminal
		if err := rows.Scan(&s.ID, &s.Source, &s.ExternalCode, &s.NameRu, &s.Lat, &s.Lon, &s.Settlement, &s.Region, &s.TransportType); err != nil {
			return nil, err
		}
		s.HasCoords = true
		out = append(out, s)
	}
	return out, rows.Err()
}

func (t *pgTxStore) UpsertTerminalAlias(ctx context.Context, a store.TerminalAliasRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO terminal_aliases(terminal_id, alias, lang, source, observed_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(terminal_id, alias, lang) DO UPDATE SET source=EXCLUDED.source, observed_at=now()`, a.TerminalID, a.Alias, a.Lang, nullSource(a.Source))
	return err
}

func (t *pgTxStore) UpsertAttributeState(ctx context.Context, a store.AttributeStateRow) error {
	origin := a.Origin
	if origin == "" {
		origin = "live"
	}
	_, err := t.tx.Exec(ctx, `INSERT INTO attribute_state(entity_type, entity_id, field, value, source, confidence, observed_at, actor_id, origin, sync_run_id) VALUES($1,$2,$3,to_jsonb($4::text),$5,$6,now(),$7,$8,$9) ON CONFLICT(entity_type, entity_id, field, source) DO UPDATE SET value=EXCLUDED.value, confidence=EXCLUDED.confidence, observed_at=now(), actor_id=EXCLUDED.actor_id, origin=EXCLUDED.origin, sync_run_id=EXCLUDED.sync_run_id`, a.EntityType, a.EntityID, a.Field, a.Value, a.Source, a.Confidence, a.ActorID, origin, a.SyncRunID)
	return err
}

func (t *pgTxStore) CreateSyncRun(ctx context.Context, r store.SyncRunRow) (int64, error) {
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO sync_runs(plan_id, kind, input_sha, tag, state, summary) VALUES($1,$2,$3,$4,'running',$5::jsonb) RETURNING id`, r.PlanID, r.Kind, r.InputSHA, r.Tag, summaryOrEmpty(r.Summary)).Scan(&id)
	return id, err
}

func (t *pgTxStore) FinishSyncRun(ctx context.Context, id int64, state, summary string) error {
	_, err := t.tx.Exec(ctx, `UPDATE sync_runs SET state=$2, finished_at=now(), summary=$3::jsonb WHERE id=$1`, id, state, summaryOrEmpty(summary))
	return err
}

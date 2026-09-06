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

func emptyToNil(s string) any {
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
	_, err := p.pool.Exec(ctx, `INSERT INTO attribute_state(entity_type, entity_id, field, value, source, confidence, observed_at, actor_id, origin, sync_run_id) VALUES($1,$2,$3,to_jsonb($4::text),$5,$6,now(),$7,$8,$9) ON CONFLICT(entity_type, entity_id, field, source) DO UPDATE SET value=EXCLUDED.value, confidence=EXCLUDED.confidence, observed_at=CASE WHEN attribute_state.value IS DISTINCT FROM EXCLUDED.value OR attribute_state.confidence IS DISTINCT FROM EXCLUDED.confidence THEN now() ELSE attribute_state.observed_at END, actor_id=EXCLUDED.actor_id, origin=EXCLUDED.origin, sync_run_id=EXCLUDED.sync_run_id`, a.EntityType, a.EntityID, a.Field, a.Value, a.Source, a.Confidence, a.ActorID, origin, a.SyncRunID)
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
		var tail string
		var args []any
		if r.HasCoords() {
			tail = `ST_SetSRID(ST_MakePoint($6,$5),4326)::geography,$7,$8,$9`
			args = []any{runID, r.Source, code, r.NameRu, lat, lon, ex["settlement"], ex["region"], ex["transport_type"]}
		} else {
			tail = `NULL,$5,$6,$7`
			args = []any{runID, r.Source, code, r.NameRu, ex["settlement"], ex["region"], ex["transport_type"]}
		}
		q := fmt.Sprintf(`INSERT INTO staging_terminals(run_id, source, source_code, name_ru, geom, settlement, region, transport_type) VALUES($1,$2,$3,$4,%s) ON CONFLICT(run_id, source, source_code) DO UPDATE SET name_ru=EXCLUDED.name_ru, geom=EXCLUDED.geom, settlement=EXCLUDED.settlement, region=EXCLUDED.region, transport_type=EXCLUDED.transport_type`, tail)
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
	_, err := t.tx.Exec(ctx, `INSERT INTO attribute_state(entity_type, entity_id, field, value, source, confidence, observed_at, actor_id, origin, sync_run_id) VALUES($1,$2,$3,to_jsonb($4::text),$5,$6,now(),$7,$8,$9) ON CONFLICT(entity_type, entity_id, field, source) DO UPDATE SET value=EXCLUDED.value, confidence=EXCLUDED.confidence, observed_at=CASE WHEN attribute_state.value IS DISTINCT FROM EXCLUDED.value OR attribute_state.confidence IS DISTINCT FROM EXCLUDED.confidence THEN now() ELSE attribute_state.observed_at END, actor_id=EXCLUDED.actor_id, origin=EXCLUDED.origin, sync_run_id=EXCLUDED.sync_run_id`, a.EntityType, a.EntityID, a.Field, a.Value, a.Source, a.Confidence, a.ActorID, origin, a.SyncRunID)
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

func (p *PostgresStore) EnsureSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO sync_chunks(run_id, entity, chunk_key, state) VALUES($1,$2,$3,'pending') ON CONFLICT(run_id, entity, chunk_key) DO NOTHING RETURNING id`, runID, entity, chunkKey).Scan(&id)
	if err == nil {
		return id, nil
	}
	err = p.pool.QueryRow(ctx, `SELECT id FROM sync_chunks WHERE run_id=$1 AND entity=$2 AND chunk_key=$3`, runID, entity, chunkKey).Scan(&id)
	return id, err
}

func (p *PostgresStore) GetSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (store.SyncChunkRow, bool) {
	if p.pool == nil {
		return store.SyncChunkRow{}, false
	}
	var r store.SyncChunkRow
	err := p.pool.QueryRow(ctx, `SELECT id, run_id, entity, chunk_key, state, coalesce(plan_id_done,''), attempts, coalesce(last_error,'') FROM sync_chunks WHERE run_id=$1 AND entity=$2 AND chunk_key=$3`, runID, entity, chunkKey).Scan(&r.ID, &r.RunID, &r.Entity, &r.ChunkKey, &r.State, &r.PlanIDDone, &r.Attempts, &r.LastError)
	if err != nil {
		return store.SyncChunkRow{}, false
	}
	return r, true
}

func (p *PostgresStore) CompleteSyncChunk(ctx context.Context, id int64, planIDDone, state, lastError string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `UPDATE sync_chunks SET state=$2, plan_id_done=$3, last_error=nullif($4,''), attempts=attempts+1 WHERE id=$1`, id, state, planIDDone, lastError)
	return err
}

func (t *pgTxStore) EnsureSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (int64, error) {
	return t.parent.EnsureSyncChunk(ctx, runID, entity, chunkKey)
}

func (t *pgTxStore) GetSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (store.SyncChunkRow, bool) {
	return t.parent.GetSyncChunk(ctx, runID, entity, chunkKey)
}

func (t *pgTxStore) CompleteSyncChunk(ctx context.Context, id int64, planIDDone, state, lastError string) error {
	return t.parent.CompleteSyncChunk(ctx, id, planIDDone, state, lastError)
}

func (p *PostgresStore) ListAttributeStates(ctx context.Context, entityType string, entityID int64) ([]store.AttributeStateRow, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT field, value #>> '{}', source, confidence, origin, actor_id, sync_run_id FROM attribute_state WHERE entity_type=$1 AND entity_id=$2`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AttributeStateRow
	for rows.Next() {
		var r store.AttributeStateRow
		r.EntityType = entityType
		r.EntityID = entityID
		if err := rows.Scan(&r.Field, &r.Value, &r.Source, &r.Confidence, &r.Origin, &r.ActorID, &r.SyncRunID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *pgTxStore) ListAttributeStates(ctx context.Context, entityType string, entityID int64) ([]store.AttributeStateRow, error) {
	return t.parent.ListAttributeStates(ctx, entityType, entityID)
}

func (p *PostgresStore) ListLegacyTerminals(ctx context.Context) ([]store.LegacyTerminalRow, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT t.id, coalesce(tn_ru.name,''), coalesce(tn_en.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), coalesce(t.tz,'') FROM terminals t LEFT JOIN terminal_names tn_ru ON tn_ru.terminal_id=t.id AND tn_ru.lang='ru' LEFT JOIN terminal_names tn_en ON tn_en.terminal_id=t.id AND tn_en.lang='en' WHERE NOT EXISTS (SELECT 1 FROM attribute_state a WHERE a.entity_type='terminal' AND a.entity_id=t.id AND a.sync_run_id IN (SELECT id FROM sync_runs WHERE kind='skeleton')) ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.LegacyTerminalRow
	var ids []int64
	for rows.Next() {
		var r store.LegacyTerminalRow
		if err := rows.Scan(&r.ID, &r.NameRu, &r.NameEn, &r.Lat, &r.Lon, &r.Tz); err != nil {
			return nil, err
		}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	idents, err := p.terminalIdentifiers(ctx, ids)
	if err != nil {
		return nil, err
	}
	votes, err := p.terminalVotes(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Identifiers = idents[out[i].ID]
		out[i].Votes = votes[out[i].ID]
	}
	return out, nil
}

func (p *PostgresStore) ListSkeletonTerminals(ctx context.Context) ([]store.SkeletonTerminalRow, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.enrichment_status, coalesce(tt.tags->>'settlement','') FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru' LEFT JOIN terminal_tags tt ON tt.terminal_id=t.id WHERE EXISTS (SELECT 1 FROM attribute_state a WHERE a.entity_type='terminal' AND a.entity_id=t.id AND a.sync_run_id IN (SELECT id FROM sync_runs WHERE kind='skeleton')) ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SkeletonTerminalRow
	var ids []int64
	for rows.Next() {
		var r store.SkeletonTerminalRow
		if err := rows.Scan(&r.ID, &r.NameRu, &r.Lat, &r.Lon, &r.EnrichmentStatus, &r.Settlement); err != nil {
			return nil, err
		}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	idents, err := p.terminalIdentifiers(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Identifiers = idents[out[i].ID]
	}
	return out, nil
}

func (t *pgTxStore) ListLegacyTerminals(ctx context.Context) ([]store.LegacyTerminalRow, error) {
	return t.parent.ListLegacyTerminals(ctx)
}

func (t *pgTxStore) ListSkeletonTerminals(ctx context.Context) ([]store.SkeletonTerminalRow, error) {
	return t.parent.ListSkeletonTerminals(ctx)
}

func (p *PostgresStore) terminalIdentifiers(ctx context.Context, ids []int64) (map[int64][]model.AdaptedIdentifier, error) {
	out := map[int64][]model.AdaptedIdentifier{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT terminal_id, system, code_type, code FROM terminal_identifiers WHERE terminal_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid int64
		var id model.AdaptedIdentifier
		if err := rows.Scan(&tid, &id.System, &id.CodeType, &id.Code); err != nil {
			return nil, err
		}
		out[tid] = append(out[tid], id)
	}
	return out, rows.Err()
}

func (p *PostgresStore) terminalVotes(ctx context.Context, ids []int64) (map[int64][]store.ProvenanceVote, error) {
	out := map[int64][]store.ProvenanceVote{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT entity_id, source, confidence, extract(epoch from observed_at)::bigint FROM provenance WHERE entity_type='terminal' AND entity_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var eid int64
		var v store.ProvenanceVote
		if err := rows.Scan(&eid, &v.Source, &v.Confidence, &v.ObservedAt); err != nil {
			return nil, err
		}
		out[eid] = append(out[eid], v)
	}
	return out, rows.Err()
}

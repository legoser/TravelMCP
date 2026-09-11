package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// terminalState — точный статус терминала для диагностики ручных операций.
type terminalState struct {
	exists bool
	locked bool
	dead   bool // тумстоун: valid_to проставлен (удалён или слит)
	merged bool // есть redirect в terminal_merges (слит в другой)
	newID  int64
}

func (p *PostgresStore) terminalState(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, id int64) (terminalState, error) {
	var st terminalState
	err := q.QueryRow(ctx, `SELECT is_locked, valid_to IS NOT NULL FROM terminals WHERE id=$1`, id).Scan(&st.exists, &st.dead)
	if err == pgx.ErrNoRows {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.exists = true
	err = q.QueryRow(ctx, `SELECT new_id FROM terminal_merges WHERE old_id=$1`, id).Scan(&st.newID)
	if err == nil {
		st.merged = true
	} else if err != pgx.ErrNoRows {
		return st, err
	}
	_ = q.QueryRow(ctx, `SELECT is_locked FROM terminals WHERE id=$1`, id).Scan(&st.locked)
	return st, nil
}

func (st terminalState) describe(id int64) string {
	switch {
	case !st.exists:
		return fmt.Sprintf("терминал %d не существует", id)
	case st.merged:
		return fmt.Sprintf("терминал %d уже слит в %d", id, st.newID)
	case st.dead:
		return fmt.Sprintf("терминал %d уже удалён (тумстоун valid_to)", id)
	default:
		return fmt.Sprintf("терминал %d существует", id)
	}
}

// MergeTerminals — операторское слияние дубликатов по плану §3.1:
// old_id тумстоунится (SCD2 valid_to), все ссылки репойнтятся на new_id,
// карта redirect плоская (переписывает старые перенаправления), история
// слияний остаётся в terminal_merges. is_locked — барьер только для
// автоматики; ручная операция оператора выполняется безусловно.
func (p *PostgresStore) MergeTerminals(ctx context.Context, oldID, newID int64, reason string, actorID *int64) error {
	if p.pool == nil {
		return errNotImplemented
	}
	if oldID == newID {
		return fmt.Errorf("merge: old_id=%d equals new_id", oldID)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	oldSt, err := p.terminalState(ctx, tx, oldID)
	if err != nil {
		return err
	}
	if !oldSt.exists || oldSt.merged || oldSt.dead {
		return fmt.Errorf("merge: дубль (old) не годится — %s", oldSt.describe(oldID))
	}
	newSt, err := p.terminalState(ctx, tx, newID)
	if err != nil {
		return err
	}
	if !newSt.exists || newSt.merged || newSt.dead {
		return fmt.Errorf("merge: целевой (new) не годится — %s", newSt.describe(newID))
	}
	// Flatten карты redirect: старые перенаправления, ведущие на oldID,
	// переписываются на newID, чтобы ResolveTerminalID оставался bounded.
	if _, err := tx.Exec(ctx, `UPDATE terminal_merges SET new_id=$2 WHERE new_id=$1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO terminal_merges(old_id, new_id, reason, actor_id) VALUES($1,$2,$3,$4) ON CONFLICT(old_id) DO UPDATE SET new_id=EXCLUDED.new_id, reason=EXCLUDED.reason, at=now(), actor_id=EXCLUDED.actor_id`, oldID, newID, reason, actorID); err != nil {
		return err
	}
	// Репойнт зависимых таблиц. Сначала у old удаляются строки, конфликтующие
	// с new по естественному ключу (в пользу new: подтверждённые данные не
	// перезаписываются), затем репойнт — UNIQUE/PK не нарушаются.
	if _, err := tx.Exec(ctx, `DELETE FROM terminal_identifiers a USING terminal_identifiers b WHERE a.terminal_id=$1 AND b.terminal_id=$2 AND a.system=b.system AND a.code=b.code`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE terminal_identifiers SET terminal_id=$2 WHERE terminal_id=$1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM terminal_aliases a USING terminal_aliases b WHERE a.terminal_id=$1 AND b.terminal_id=$2 AND a.alias=b.alias AND a.lang=b.lang`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE terminal_aliases SET terminal_id=$2 WHERE terminal_id=$1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM terminal_names a USING terminal_names b WHERE a.terminal_id=$1 AND b.terminal_id=$2 AND a.lang=b.lang`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE terminal_names SET terminal_id=$2 WHERE terminal_id=$1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM attribute_state a USING attribute_state b WHERE a.entity_type='terminal' AND a.entity_id=$1 AND b.entity_type='terminal' AND b.entity_id=$2 AND a.field=b.field AND a.source=b.source`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE attribute_state SET entity_id=$2 WHERE entity_type='terminal' AND entity_id=$1`, oldID, newID); err != nil {
		return err
	}
	// provenance: у new остаются свои голоса (не перезаписываются), у old
	// копируются недостающие (ON CONFLICT DO NOTHING); сам old сохраняет
	// свои строки — история источника не стирается.
	if _, err := tx.Exec(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, raw, actor_id, channel) SELECT entity_type, $2, source, confidence, observed_at, raw, actor_id, channel FROM provenance WHERE entity_type='terminal' AND entity_id=$1 ON CONFLICT(entity_type, entity_id, source) DO NOTHING`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE stops_canonical SET terminal_id=$2 WHERE terminal_id=$1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM terminal_tags a USING terminal_tags b WHERE a.terminal_id=$1 AND b.terminal_id=$2`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE terminal_tags SET terminal_id=$2 WHERE terminal_id=$1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM review_queue a USING review_queue b WHERE a.entity_type='terminal' AND a.entity_id=$1 AND b.entity_type='terminal' AND b.entity_id=$2 AND a.reason=b.reason`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE review_queue SET entity_id=$2 WHERE entity_type='terminal' AND entity_id=$1`, oldID, newID); err != nil {
		return err
	}
	// Тумстоун old (SCD2): строка остаётся для аудита слияний.
	if _, err := tx.Exec(ctx, `UPDATE terminals SET valid_to=CURRENT_DATE WHERE id=$1 AND valid_to IS NULL`, oldID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResetCanonicalData — операторская полная очистка канона терминалов и
// рейсов (перезагрузка с нуля после массовых дублей). Удаляет всё, что
// построено конвейером: терминалы и их зависимые таблицы, маршруты/рейсы/
// стопы, staging, историю синков и ревью. Каркас (users, api_keys, jobs,
// providers, places, geocode_cache, api_quotas) не трогается; кэш Яндекса
// на диске не трогается — переиспользуется для offline-прогона.
// На текущей фазе операторская операция (audit), фаза 5 добавит роли.
func (p *PostgresStore) ResetCanonicalData(ctx context.Context, actorID *int64, reason string) (map[string]int, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	counts := map[string]int{}
	// Порядок по FK: сначала листья, затем корни. stops_canonical/stop_times
	// каскадом от terminals, но чистим явно для симметричного подсчёта.
	stmts := []struct {
		name string
		sql  string
	}{
		{"stop_times", `DELETE FROM stop_times`},
		{"stops_canonical", `DELETE FROM stops_canonical`},
		{"stop_names", `DELETE FROM stop_names`},
		{"stop_zones", `DELETE FROM stop_zones`},
		{"transfers", `DELETE FROM transfers`},
		{"trip_sources", `DELETE FROM trip_sources`},
		{"trips", `DELETE FROM trips`},
		{"routes", `DELETE FROM routes`},
		{"frequencies", `DELETE FROM frequencies`},
		{"services", `DELETE FROM services`},
		{"service_days", `DELETE FROM service_days`},
		{"service_exceptions", `DELETE FROM service_exceptions`},
		{"staging_trips", `DELETE FROM staging_trips`},
		{"staging_terminals", `DELETE FROM staging_terminals`},
		{"terminal_identifiers", `DELETE FROM terminal_identifiers`},
		{"terminal_aliases", `DELETE FROM terminal_aliases`},
		{"terminal_names", `DELETE FROM terminal_names`},
		{"terminal_tags", `DELETE FROM terminal_tags`},
		{"terminal_merges", `DELETE FROM terminal_merges`},
		{"attribute_state", `DELETE FROM attribute_state WHERE entity_type IN ('terminal','stop','route','trip')`},
		{"review_queue", `DELETE FROM review_queue`},
		{"provenance", `DELETE FROM provenance WHERE entity_type IN ('terminal','stop','route','trip')`},
		{"provenance_history", `DELETE FROM provenance_history WHERE entity_type IN ('terminal','stop','route','trip')`},
		{"sync_chunks", `DELETE FROM sync_chunks`},
		{"sync_runs", `DELETE FROM sync_runs`},
		{"terminals", `DELETE FROM terminals`},
	}
	for _, st := range stmts {
		ct, err := tx.Exec(ctx, st.sql)
		if err != nil {
			return nil, fmt.Errorf("reset %s: %w", st.name, err)
		}
		counts[st.name] = int(ct.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return counts, nil
}

// ResolveTerminalID — карта redirect через terminal_merges, bounded
// (защита от циклов лимитом прыжков).
func (p *PostgresStore) ResolveTerminalID(ctx context.Context, id int64) (int64, error) {
	if p.pool == nil {
		return id, nil
	}
	const maxHops = 32
	cur := id
	for range maxHops {
		var next int64
		err := p.pool.QueryRow(ctx, `SELECT new_id FROM terminal_merges WHERE old_id=$1`, cur).Scan(&next)
		if err == pgx.ErrNoRows {
			return cur, nil
		}
		if err != nil {
			return cur, err
		}
		cur = next
	}
	return cur, fmt.Errorf("resolve terminal id: цепочка перенаправлений %d зациклена", id)
}

// UnlockTerminal — снятие ручного подтверждения оператором: терминал
// возвращается в автоматический конвейер (апрув мог быть ошибочным, и
// оператор должен иметь путь откатить своё же решение). provenance manual
// голоса не удаляются — история подтверждения сохраняется, audit пишется.
func (p *PostgresStore) UnlockTerminal(ctx context.Context, id int64, actorID *int64) error {
	if p.pool == nil {
		return errNotImplemented
	}
	st, err := p.terminalState(ctx, p.pool, id)
	if err != nil {
		return err
	}
	if !st.exists || st.merged || st.dead {
		return fmt.Errorf("unlock: %s", st.describe(id))
	}
	if !st.locked {
		return fmt.Errorf("unlock: терминал %d не залочен", id)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE terminals SET is_locked=false WHERE id=$1 AND is_locked`, id); err != nil {
		return err
	}
	if actorID != nil {
		_, _ = p.pool.Exec(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, actor_id, channel) VALUES('terminal',$1,'manual',1.0,$2,$3,'local_file')`, id, time.Now(), actorID)
	}
	return nil
}

// DeleteTerminal — операторское удаление: тумстоун SCD2 (valid_to),
// не физическое. is_locked — барьер только для автоматики: ручное
// удаление оператором выполняется безусловно, история сохраняется.
func (p *PostgresStore) DeleteTerminal(ctx context.Context, id int64, actorID *int64) error {
	if p.pool == nil {
		return errNotImplemented
	}
	st, err := p.terminalState(ctx, p.pool, id)
	if err != nil {
		return err
	}
	if !st.exists {
		return fmt.Errorf("delete: терминал %d не существует", id)
	}
	if st.merged {
		return fmt.Errorf("delete: %s", st.describe(id))
	}
	if st.dead {
		return fmt.Errorf("delete: терминал %d уже удалён (тумстоун valid_to)", id)
	}
	ct, err := p.pool.Exec(ctx, `UPDATE terminals SET valid_to=CURRENT_DATE, is_locked=false WHERE id=$1 AND valid_to IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("delete: терминал %d не найден или уже удалён", id)
	}
	if actorID != nil {
		_, _ = p.pool.Exec(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, actor_id, channel) VALUES('terminal',$1,'manual',1.0,$2,$3,'local_file')`, id, time.Now(), actorID)
	}
	return nil
}

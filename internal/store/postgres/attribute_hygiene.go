package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	store "travelmcp/internal/store"
)

// FinalizeAndGCAttributeState — «freshness sweep» Фазы 5 для attribute_state:
// (a) finalize — строки без actor_id (не ручные), не seed, наблюдавшиеся
// позже cutoff НЕ бывшие предметом конкуренции, уезжают append-only в
// provenance_history и покидают «текущих конкурентов»; (b) GC — seed-строка
// поля, у которого есть live-конкурент, вытесняется (seed — никогда голос
// за finalize, §3.1). Ручные правки (actor_id) не трогаются — барьер поля.
//
// Идемпотентно: повторный прогон не находит ни стабильных (удалены), ни
// вытесненных seed.
func (p *PostgresStore) FinalizeAndGCAttributeState(ctx context.Context, retention time.Duration) (store.AttributeSweepResult, error) {
	var res store.AttributeSweepResult
	if p.pool == nil {
		return res, errNotImplemented
	}
	if retention <= 0 {
		return res, fmt.Errorf("attribute hygiene: retention должен быть положительным (got %s)", retention)
	}
	cutoff := time.Now().Add(-retention)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// finalize: стабильные конкуренты → provenance_history (append-only)
	tag, err := tx.Exec(ctx, `INSERT INTO provenance_history(entity_type, entity_id, source, confidence, observed_at, sync_run_id)
		SELECT a.entity_type, a.entity_id, a.source, a.confidence, a.observed_at, a.sync_run_id
		FROM attribute_state a
		WHERE a.actor_id IS NULL AND a.origin <> 'seed' AND a.observed_at < $1`, cutoff)
	if err != nil {
		return res, err
	}
	res.FinalizedToHistory = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `DELETE FROM attribute_state a WHERE a.actor_id IS NULL AND a.origin <> 'seed' AND a.observed_at < $1`, cutoff)
	if err != nil {
		return res, err
	}
	if n := int(tag.RowsAffected()); n != res.FinalizedToHistory {
		return res, fmt.Errorf("attribute hygiene: несходимость finalize: в history %d, удалено %d", res.FinalizedToHistory, n)
	}
	// GC: seed вытеснен live-конкурентом того же поля
	tag, err = tx.Exec(ctx, `DELETE FROM attribute_state s
		WHERE s.origin = 'seed'
		  AND EXISTS (SELECT 1 FROM attribute_state l
		              WHERE l.entity_type = s.entity_type AND l.entity_id = s.entity_id
		                AND l.field = s.field AND l.source <> s.source AND l.origin = 'live')`)
	if err != nil {
		return res, err
	}
	res.GCSupersededSeed = int(tag.RowsAffected())
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM attribute_state`).Scan(&res.RemainingLive)
	return res, nil
}

// StaleAttributesCounters — KPI §8: возраст живых конкурентов (драйвер
// v_stale_attributes): счётчики строк attribute_state старше порога свежести,
// с разрезом по field/source. Сложение причин для ресинка — на стороне
// потребителя (дашборд/алерты).
func (p *PostgresStore) StaleAttributesCounters(ctx context.Context, olderThan time.Duration) (map[string]int, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT field || '/' || source, count(*)
		FROM attribute_state WHERE observed_at < $1 GROUP BY 1`, time.Now().Add(-olderThan))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// AuditPossibleMerges — аудит §2 Фазы 5: кандидаты слияния по двум сигналам:
// (a) мульти-тип терминал ({bus,rail} — часто честный интермодальный хаб,
// подлежит проверке оператором); (b) пара терминалов ближе radiusM с
// пересекающимися transport_types и непохожими именами (дробление скелета).
// Возвращает кандидатов; вызовавший решает, отправлять ли в review_queue
// (SaveReviewQueue{possible_merge, fingerprint=merge:a:b}).
func (p *PostgresStore) AuditPossibleMerges(ctx context.Context, radiusM float64, limit int) ([]store.MergeAuditCandidate, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	if limit <= 0 {
		limit = 500
	}
	if radiusM <= 0 {
		radiusM = 300
	}
	rows, err := p.pool.Query(ctx, `
WITH pairs AS (
  SELECT a.id a_id, coalesce(na.name,'(без имени)') a_name, array_to_string(a.transport_types,'+') a_types,
         b.id b_id, coalesce(nb.name,'(без имени)') b_name, array_to_string(b.transport_types,'+') b_types,
         ST_Distance(a.geom, b.geom) dist,
         (SELECT count(*) FROM unnest(a.transport_types) x WHERE x = ANY(b.transport_types)) shared
  FROM terminals a
  JOIN terminals b ON b.id > a.id AND b.valid_to IS NULL
       AND ST_DWithin(a.geom, b.geom, $1)
       AND a.transport_types && b.transport_types
  LEFT JOIN terminal_names na ON na.terminal_id = a.id AND na.lang='ru'
  LEFT JOIN terminal_names nb ON nb.terminal_id = b.id AND nb.lang='ru'
  WHERE a.valid_to IS NULL AND array_length(a.transport_types,1) > 0
)
SELECT a_id, a_name, a_types, b_id, b_name, b_types, dist::double precision, shared::int
FROM pairs ORDER BY dist LIMIT $2`, radiusM, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.MergeAuditCandidate
	for rows.Next() {
		var c store.MergeAuditCandidate
		if err := rows.Scan(&c.TerminalID, &c.Name, &c.TransportTypes, &c.NeighborID, &c.NeighborName, &c.NeighborTypes, &c.DistanceM, &c.SharedTypes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// store.TransportTypesRecomputeResult — сверка материализованного агрегата
// RecomputeTransportTypes — ночная сверка §3.1: aggregate transport_types
// должен содержать фактические типы стопов (stops_canonical.stop_type) и
// режимы маршрутов через трипы терминала. Bulk-пути записи агрегата не
// проходят через триггер — сверка ловит дрейф. Правило объединения, не
// замещения: факт ДОБАВЛЯЕТ типы к агрегату скелета (stored ∪ fact) — типы
// скелета (OSM/Yandex: rail/flight у интермодальных хабов) легитимны до
// появления соответствующих stop_times и не должны срезаться временным
// отсутствием ж/д-рейсов. Отчёт ByDelta честно показывает, что добавлено.
func (p *PostgresStore) RecomputeTransportTypes(ctx context.Context) (store.TransportTypesRecomputeResult, error) {
	res := store.TransportTypesRecomputeResult{ByDelta: map[string]int{}}
	if p.pool == nil {
		return res, errNotImplemented
	}
	// Early-dedup: (stop_id, mode) агрегируется ДО join со стопами (3.5M →
	// ~25k строк), поэтому дедуп через UNION работает на порядок дешевле,
	// а не по факту сортировки 3.5M строк. EXCEPT-проверка на live-БД:
	// результат идентичен прежней LATERAL-форме (12.3s → ~1.0s).
	rows, err := p.pool.Query(ctx, `
WITH modes_per_stop AS (
  SELECT st.stop_id, r.mode::text AS mode
  FROM stop_times st
  JOIN trips tr ON st.trip_id = tr.id AND tr.valid_to IS NULL
  JOIN routes r ON r.id = tr.route_id AND r.valid_to IS NULL
  GROUP BY st.stop_id, r.mode::text
),
fact AS (
  SELECT u.terminal_id, string_agg(u.m, '+' ORDER BY u.m) AS fact
  FROM (
    SELECT sc.terminal_id, sc.stop_type::text AS m
    FROM stops_canonical sc WHERE sc.stop_type IS NOT NULL
    UNION
    SELECT sc.terminal_id, mps.mode
    FROM stops_canonical sc JOIN modes_per_stop mps ON mps.stop_id = sc.id
  ) u
  GROUP BY u.terminal_id
)
SELECT t.id,
       array_to_string(t.transport_types, '+') AS stored,
       coalesce(f.fact, '') AS fact
FROM terminals t
LEFT JOIN fact f ON f.terminal_id = t.id
WHERE t.valid_to IS NULL`)
	if err != nil {
		return res, err
	}
	type delta struct {
		id   int64
		fact string
	}
	var updates []delta
	for rows.Next() {
		var id int64
		var stored, fact string
		if err := rows.Scan(&id, &stored, &fact); err != nil {
			return res, err
		}
		res.Checked++
		if fact == "" {
			continue
		}
		merged := unionTypes(stored, fact)
		if merged != stored {
			updates = append(updates, delta{id, merged})
			res.Updated++
			res.ByDelta[stored+"→"+merged]++
		}
	}
	rows.Close()
	for _, u := range updates {
		arr := strings.Split(u.fact, "+")
		if _, err := p.pool.Exec(ctx, `UPDATE terminals SET transport_types = $2 WHERE id = $1`, u.id, arr); err != nil {
			return res, err
		}
	}
	return res, nil
}

// unionTypes — stored ∪ fact через '+'; сортировка стабильна (lex), дедуп.
func unionTypes(stored, fact string) string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(stored+"+"+fact, "+") {
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	sort.Strings(out)
	return strings.Join(out, "+")
}

// SampleMarginalMatches — acceptance sampling для FP-оценки матчинга (§8):
// stop_times с match_score в полосе [lo, hi) — маргинальные verified-решения,
// ближайшие к порогу сверху. Оператор проверяет N сэмплов руками, FP-рейт
// попадает в KPI. Порядок — по возрастанию score (самые сомнительные первыми).
func (p *PostgresStore) SampleMarginalMatches(ctx context.Context, lo, hi float64, limit int) ([]store.MarginalMatchSample, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	if limit <= 0 {
		limit = 50
	}
	if hi <= lo {
		return nil, fmt.Errorf("sample marginal: hi (%.3f) должен быть > lo (%.3f)", hi, lo)
	}
	rows, err := p.pool.Query(ctx, `
SELECT st.trip_id, st.stop_id, coalesce(sn.name, '(без имени)'), st.match_score, coalesce(st.match_method,''), st.is_provisional
FROM stop_times st
LEFT JOIN stop_names sn ON sn.stop_id = st.stop_id AND sn.lang='ru'
WHERE st.match_score >= $1 AND st.match_score < $2
ORDER BY st.match_score ASC LIMIT $3`, lo, hi, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.MarginalMatchSample
	for rows.Next() {
		var s store.MarginalMatchSample
		if err := rows.Scan(&s.TripID, &s.StopID, &s.StopName, &s.MatchScore, &s.MatchMethod, &s.IsProvisional); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

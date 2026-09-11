package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	store "travelmcp/internal/store"
)

// ListTerminalIDByCode — терминал по идентификатору (system+code), без
// создания; для идемпотентных GTFS-ресинков (стоп уже терминал).
func (p *PostgresStore) ListTerminalIDByCode(ctx context.Context, system, code string) (int64, bool) {
	if p.pool == nil {
		return 0, false
	}
	var id int64
	if err := p.pool.QueryRow(ctx, `SELECT terminal_id FROM terminal_identifiers WHERE system=$1 AND code=$2 ORDER BY is_primary DESC, terminal_id LIMIT 1`, system, code).Scan(&id); err != nil {
		return 0, false
	}
	return id, true
}

// TerminalStagingRegion — регион терминала из последнего staging-кэша
// (скелет сохранял region при импорте дампа; канон-теги могут быть
// пустыми до enrichment). Пустая строка — регион неизвестен.
func (p *PostgresStore) TerminalStagingRegion(ctx context.Context, terminalID int64) string {
	if p.pool == nil {
		return ""
	}
	var region string
	_ = p.pool.QueryRow(ctx, `SELECT s.region FROM staging_terminals s JOIN terminal_identifiers i ON i.code=s.source_code AND i.system='yandex' WHERE i.terminal_id=$1 ORDER BY s.id DESC LIMIT 1`, terminalID).Scan(&region)
	return region
}

// AppendStopTimes — батч-вставка stop_times (pgx.Batch; импорт GTFS:
// миллионы строк, построчные INSERT недопустимы). Конфликт = повторный
// импорт той же строки: UPDATE оседает идемпотентно.
func (p *PostgresStore) AppendStopTimes(ctx context.Context, rows []store.StopTimeRow) error {
	if p.pool == nil {
		return errNotImplemented
	}
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, st := range rows {
		batch.Queue(`INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, is_provisional) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(trip_id, seq) DO UPDATE SET stop_id=EXCLUDED.stop_id, arrival=EXCLUDED.arrival, departure=EXCLUDED.departure`,
			st.TripID, st.StopID, st.Seq, st.Arrival, st.Departure, st.IsProvisional)
	}
	if err := p.pool.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("append stop_times batch: %w", err)
	}
	return nil
}

// ReadServiceDays — дни недели сервиса (для денормализованной строки
// trips.service_days при импорте GTFS).
func (p *PostgresStore) ReadServiceDays(ctx context.Context, serviceID int64) ([]int, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT weekday FROM service_days WHERE service_id=$1 ORDER BY weekday`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var wd int
		if err := rows.Scan(&wd); err != nil {
			return nil, err
		}
		out = append(out, wd)
	}
	return out, rows.Err()
}

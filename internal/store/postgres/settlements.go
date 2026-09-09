package postgres

import (
	"context"

	"travelmcp/internal/geo"
)

// ListSettlements — населённые пункты из канона (terminal_tags.settlement) для
// рантайм-газетира. Представитель поселения: терминал с живыми рейсами
// (stop_times несённых валидными трипами) → bus_station → station → id.
// Наличие живых рейсов важнее типа: автовокзал без расписаний бесполезен
// как точка маршрута, даже если он «главнее».
func (p *PostgresStore) ListSettlements(ctx context.Context) ([]geo.Settlement, error) {
	const q = `
WITH reps AS (
  SELECT DISTINCT ON (tt.tags->>'settlement')
    tt.tags->>'settlement' AS name,
    ST_Y(t.geom::geometry)  AS lat,
    ST_X(t.geom::geometry)  AS lon
  FROM terminal_tags tt
  JOIN terminals t ON t.id = tt.terminal_id
  WHERE t.geom IS NOT NULL
    AND tt.tags->>'settlement' <> ''
    AND t.valid_to IS NULL
  ORDER BY 1,
    (SELECT count(*) FROM stop_times st
       JOIN trips tr ON st.trip_id = tr.id AND tr.valid_to IS NULL
       JOIN stops_canonical sc ON sc.id = st.stop_id
       WHERE sc.terminal_id = t.id) DESC,
    CASE WHEN t.object_type = 'bus_station' THEN 0
         WHEN t.object_type = 'station'     THEN 1
         ELSE 2 END,
    t.id
)
SELECT name, lat, lon FROM reps ORDER BY name`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]geo.Settlement, 0, 512)
	for rows.Next() {
		var s geo.Settlement
		if err := rows.Scan(&s.Name, &s.Lat, &s.Lon); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

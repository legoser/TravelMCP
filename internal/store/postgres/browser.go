package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	store "travelmcp/internal/store"

	"github.com/jackc/pgx/v5"
)

func (p *PostgresStore) ListRoutesAdmin(ctx context.Context, limit, offset int, q string) ([]map[string]any, int, error) {
	if p.pool == nil {
		return []map[string]any{}, 0, nil
	}
	q = strings.TrimSpace(q)
	hasQ := q != ""
	var total int
	if hasQ {
		_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM routes r WHERE r.short_name ILIKE '%' || $1 || '%' OR r.long_name ILIKE '%' || $1 || '%' OR r.external_route_code ILIKE '%' || $1 || '%'`, q).Scan(&total)
	} else {
		_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM routes`).Scan(&total)
	}
	var rows pgx.Rows
	var err error
	if hasQ {
		rows, err = p.pool.Query(ctx, `SELECT r.id, r.source_provider, r.external_route_code, coalesce(r.short_name,''), coalesce(r.long_name,''), coalesce(r.mode,''), coalesce(c.name_ru,''), to_char(r.valid_from,'YYYY-MM-DD'), to_char(r.valid_to,'YYYY-MM-DD'), (SELECT count(*) FROM trips t WHERE t.route_id=r.id AND t.valid_to IS NULL) FROM routes r LEFT JOIN carriers c ON c.id=r.carrier_id WHERE r.short_name ILIKE '%' || $3 || '%' OR r.long_name ILIKE '%' || $3 || '%' OR r.external_route_code ILIKE '%' || $3 || '%' ORDER BY r.id LIMIT $1 OFFSET $2`, limit, offset, q)
	} else {
		rows, err = p.pool.Query(ctx, `SELECT r.id, r.source_provider, r.external_route_code, coalesce(r.short_name,''), coalesce(r.long_name,''), coalesce(r.mode,''), coalesce(c.name_ru,''), to_char(r.valid_from,'YYYY-MM-DD'), to_char(r.valid_to,'YYYY-MM-DD'), (SELECT count(*) FROM trips t WHERE t.route_id=r.id AND t.valid_to IS NULL) FROM routes r LEFT JOIN carriers c ON c.id=r.carrier_id ORDER BY r.id LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var provider, code, short, long, mode, carrier string
		var validFrom, validTo *string
		var liveTrips int
		if err := rows.Scan(&id, &provider, &code, &short, &long, &mode, &carrier, &validFrom, &validTo, &liveTrips); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]any{"id": id, "provider": provider, "external_route_code": code, "short_name": short, "long_name": long, "mode": mode, "carrier": carrier, "valid_from": validFrom, "valid_to": validTo, "live_trips": liveTrips})
	}
	return out, total, nil
}

func (p *PostgresStore) ListTripsAdmin(ctx context.Context, routeID int64, limit, offset int) ([]map[string]any, int, error) {
	if p.pool == nil {
		return []map[string]any{}, 0, nil
	}
	var total int
	_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM trips WHERE route_id=$1`, routeID).Scan(&total)
	rows, err := p.pool.Query(ctx, `SELECT t.id, t.external_trip_code, coalesce(t.direction,''), t.direction_id, coalesce(t.service_days,''), coalesce(t.headsign_ru,''), t.duration_s, t.distance_m, t.valid_to, (SELECT count(*) FROM stop_times st WHERE st.trip_id=t.id), (SELECT count(*) FROM stop_times st WHERE st.trip_id=t.id AND st.is_provisional) FROM trips t WHERE t.route_id=$1 ORDER BY t.id LIMIT $2 OFFSET $3`, routeID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var code, direction, serviceDays, headsign string
		var directionID *int
		var durationS, distanceM *int
		var validTo *time.Time
		var stopCount, provisionalCount int
		if err := rows.Scan(&id, &code, &direction, &directionID, &serviceDays, &headsign, &durationS, &distanceM, &validTo, &stopCount, &provisionalCount); err != nil {
			return nil, 0, err
		}
		item := map[string]any{"id": id, "external_trip_code": code, "direction": direction, "service_days": serviceDays, "headsign_ru": headsign, "duration_s": durationS, "distance_m": distanceM, "stop_times_count": stopCount, "provisional_count": provisionalCount, "is_live": validTo == nil}
		if directionID != nil {
			item["direction_id"] = *directionID
		}
		out = append(out, item)
	}
	return out, total, nil
}

func (p *PostgresStore) GetTripStopTimes(ctx context.Context, tripID int64) ([]map[string]any, error) {
	if p.pool == nil {
		return []map[string]any{}, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT st.seq, coalesce(st.arrival, st.departure), coalesce(st.departure, st.arrival), st.pickup_type, st.drop_off_type, st.is_provisional, st.match_score, coalesce(st.match_method,''), coalesce(sn.name, tn.name, '') FROM stop_times st JOIN stops_canonical sc ON sc.id=st.stop_id LEFT JOIN stop_names sn ON sn.stop_id=sc.id AND sn.lang='ru' LEFT JOIN terminal_names tn ON tn.terminal_id=sc.terminal_id AND tn.lang='ru' WHERE st.trip_id=$1 ORDER BY st.seq`, tripID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var seq, arrival, departure, pickup, dropOff int
		var provisional bool
		var score *float64
		var method, name string
		if err := rows.Scan(&seq, &arrival, &departure, &pickup, &dropOff, &provisional, &score, &method, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"seq": seq, "arrival": arrival, "departure": departure, "pickup_type": pickup, "drop_off_type": dropOff, "is_provisional": provisional, "match_score": score, "match_method": method, "name": name})
	}
	return out, nil
}

func (p *PostgresStore) GetTerminalStats(ctx context.Context, terminalID int64) (map[string]any, error) {
	if p.pool == nil {
		return map[string]any{}, nil
	}
	var stopTimes, provisionalStops, liveTrips int
	_ = p.pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(CASE WHEN st.is_provisional THEN 1 ELSE 0 END),0) FROM stop_times st JOIN stops_canonical sc ON sc.id=st.stop_id WHERE sc.terminal_id=$1`, terminalID).Scan(&stopTimes, &provisionalStops)
	_ = p.pool.QueryRow(ctx, `SELECT count(DISTINCT st.trip_id) FROM stop_times st JOIN stops_canonical sc ON sc.id=st.stop_id JOIN trips t ON t.id=st.trip_id WHERE sc.terminal_id=$1 AND t.valid_to IS NULL`, terminalID).Scan(&liveTrips)
	return map[string]any{"stop_times": stopTimes, "provisional_stop_times": provisionalStops, "live_trips": liveTrips, "dead": stopTimes == 0}, nil
}

func (p *PostgresStore) GetTerminalSchedule(ctx context.Context, terminalID int64, date time.Time, limit int) ([]map[string]any, error) {
	if p.pool == nil {
		return []map[string]any{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	weekday := int(date.Weekday())
	dateStr := date.Format("2006-01-02")
	rows, err := p.pool.Query(ctx, `
		SELECT st.departure, coalesce(sn.name, tn.name, '') AS terminal_name, coalesce(t.headsign_ru, r.long_name, r.short_name, '') AS dest, r.mode, t.external_trip_code, t.service_days, t.id,
		       (SELECT se.exception_type FROM service_exceptions se WHERE se.service_id=t.service_id AND se.date=$3::date LIMIT 1) AS exc,
		       (SELECT sd.weekday FROM service_days sd WHERE sd.service_id=t.service_id LIMIT 1) AS has_service_days
		FROM stop_times st
		JOIN stops_canonical sc ON sc.id=st.stop_id
		JOIN trips t ON t.id=st.trip_id AND t.valid_to IS NULL
		JOIN routes r ON r.id=t.route_id
		LEFT JOIN stop_names sn ON sn.stop_id=sc.id AND sn.lang='ru'
		LEFT JOIN terminal_names tn ON tn.terminal_id=sc.terminal_id AND tn.lang='ru'
		WHERE sc.terminal_id=$1
		ORDER BY st.departure
		LIMIT $2`, terminalID, limit*3, dateStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var departure int
		var name, dest, mode, tripCode, serviceDays string
		var tripID int64
		var exc *string
		var hasServiceDays *int
		if err := rows.Scan(&departure, &name, &dest, &mode, &tripCode, &serviceDays, &tripID, &exc, &hasServiceDays); err != nil {
			return nil, err
		}
		if exc != nil {
			if *exc == "removed" {
				continue
			}
		} else if !serviceDaysMatchWeekday(serviceDays, weekday) {
			continue
		}
		out = append(out, map[string]any{"departure": departure, "terminal_name": name, "destination": dest, "mode": mode, "trip_id": tripID, "external_trip_code": tripCode, "service_days": serviceDays, "exception": exc})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (p *PostgresStore) ApproveTerminal(ctx context.Context, terminalID int64, tr store.TerminalRow, names map[string]string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	if terminalID <= 0 {
		return errNotImplemented
	}
	if tr.Lat != 0 || tr.Lon != 0 {
		if _, err := p.pool.Exec(ctx, `UPDATE terminals SET geom=ST_SetSRID(ST_MakePoint($2,$3),4326)::geography, last_verified_at=$4, is_locked=true WHERE id=$1`, terminalID, tr.Lon, tr.Lat, unixTsOrNull(tr.LastVerifiedAt)); err != nil {
			return err
		}
	} else {
		if _, err := p.pool.Exec(ctx, `UPDATE terminals SET last_verified_at=$2, is_locked=true WHERE id=$1`, terminalID, unixTsOrNull(tr.LastVerifiedAt)); err != nil {
			return err
		}
	}
	for lang, name := range names {
		if name == "" {
			continue
		}
		if _, err := p.pool.Exec(ctx, `INSERT INTO terminal_names(terminal_id, lang, name, is_primary) VALUES($1,$2,$3,true) ON CONFLICT(terminal_id, lang) DO UPDATE SET name=EXCLUDED.name`, terminalID, lang, name); err != nil {
			return err
		}
	}
	return nil
}

func (p *PostgresStore) ListTerminalsByLiveness(ctx context.Context, limit, offset int, dead string) ([]map[string]any, int, error) {
	if p.pool == nil {
		return []map[string]any{}, 0, nil
	}
	cond := ""
	args := []any{limit, offset}
	switch dead {
	case "yes":
		cond = ` WHERE t.valid_to IS NULL AND NOT EXISTS (SELECT 1 FROM stops_canonical sc JOIN stop_times st ON st.stop_id=sc.id WHERE sc.terminal_id=t.id)`
	case "no":
		cond = ` WHERE t.valid_to IS NULL AND EXISTS (SELECT 1 FROM stops_canonical sc JOIN stop_times st ON st.stop_id=sc.id WHERE sc.terminal_id=t.id)`
	default:
		cond = ` WHERE t.valid_to IS NULL`
	}
	var total int
	_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM terminals t`+cond).Scan(&total)
	rows, err := p.pool.Query(ctx, `SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.is_locked, coalesce(t.place_id,0)::bigint, (SELECT count(DISTINCT st.trip_id) FROM stops_canonical sc JOIN stop_times st ON st.stop_id=sc.id WHERE sc.terminal_id=t.id), to_char(t.valid_from,'YYYY-MM-DD'), coalesce(to_char(t.valid_to,'YYYY-MM-DD'),'') FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru'`+cond+` ORDER BY t.id LIMIT $1 OFFSET $2`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var lat, lon float64
		var locked bool
		var placeID, tripsServed int64
		var validFrom, validTo string
		if err := rows.Scan(&id, &name, &lat, &lon, &locked, &placeID, &tripsServed, &validFrom, &validTo); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "lat": lat, "lon": lon, "is_locked": locked, "place_id": placeID, "trips_served": tripsServed, "dead": tripsServed == 0, "valid_from": validFrom, "valid_to": validTo})
	}
	return out, total, nil
}

func serviceDaysMatchWeekday(serviceDays string, weekday int) bool {
	if serviceDays == "" {
		return true
	}
	for _, part := range strings.Split(serviceDays, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == weekday {
			return true
		}
	}
	return false
}

func (p *PostgresStore) ListTerminalAliases(ctx context.Context, terminalID int64) ([]store.TerminalAliasRow, error) {
	if p.pool == nil {
		return []store.TerminalAliasRow{}, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT alias, lang, coalesce(source,''), extract(epoch from observed_at)::bigint FROM terminal_aliases WHERE terminal_id=$1 ORDER BY alias`, terminalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TerminalAliasRow{}
	for rows.Next() {
		var a store.TerminalAliasRow
		a.TerminalID = terminalID
		if err := rows.Scan(&a.Alias, &a.Lang, &a.Source, &a.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (p *PostgresStore) ListTerminalReviewEntries(ctx context.Context, terminalID int64) ([]store.ReviewQueueRow, error) {
	if p.pool == nil {
		return []store.ReviewQueueRow{}, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT entity_type, entity_id, reason, score, extract(epoch from created_at)::bigint, coalesce(fingerprint,'') FROM review_queue WHERE entity_type='terminal' AND entity_id=$1 ORDER BY created_at DESC`, terminalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.ReviewQueueRow{}
	for rows.Next() {
		var r store.ReviewQueueRow
		if err := rows.Scan(&r.EntityType, &r.EntityID, &r.Reason, &r.Score, &r.CreatedAt, &r.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

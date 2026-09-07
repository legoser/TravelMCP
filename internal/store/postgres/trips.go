package postgres

import (
	"context"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

func (p *PostgresStore) FindRouteID(ctx context.Context, source, routeCode string) (int64, bool) {
	if p.pool == nil {
		return 0, false
	}
	var id int64
	if err := p.pool.QueryRow(ctx, `SELECT id FROM routes WHERE source_provider=$1 AND external_route_code=$2`, source, routeCode).Scan(&id); err != nil {
		return 0, false
	}
	return id, true
}

func (p *PostgresStore) TombstoneRoute(ctx context.Context, source, routeCode string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	var id int64
	if err := p.pool.QueryRow(ctx, `UPDATE routes SET valid_to=CURRENT_DATE WHERE source_provider=$1 AND external_route_code=$2 AND valid_to IS NULL RETURNING id`, source, routeCode).Scan(&id); err != nil {
		return nil
	}
	_, _ = p.pool.Exec(ctx, `UPDATE trips SET valid_to=now() WHERE route_id=$1 AND valid_to IS NULL`, id)
	return nil
}

func (p *PostgresStore) FindTrip(ctx context.Context, routeID int64, tripCode string) (store.TripRow, bool) {
	if p.pool == nil {
		return store.TripRow{}, false
	}
	var t store.TripRow
	var validTo *string
	err := p.pool.QueryRow(ctx, `SELECT id, route_id, provider_id, external_trip_code, coalesce(direction,''), direction_id, coalesce(service_days,''), coalesce(frequency_flag,0), coalesce(period,''), coalesce(service_id,0), duration_s, distance_m, method, valid_to::text FROM trips WHERE route_id=$1 AND external_trip_code=$2`,
		routeID, tripCode).Scan(&t.ID, &t.RouteID, &t.ProviderID, &t.ExternalTripCode, &t.Direction, &t.DirectionID, &t.ServiceDays, &t.FrequencyFlag, &t.Period, &t.ServiceID, &t.DurationS, &t.DistanceM, &t.Method, &validTo)
	if err != nil {
		return store.TripRow{}, false
	}
	t.ValidTo = validTo
	return t, true
}

func (p *PostgresStore) TombstoneTrip(ctx context.Context, tripID int64) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `UPDATE trips SET valid_to=now() WHERE id=$1 AND valid_to IS NULL`, tripID)
	return err
}

func (p *PostgresStore) DeleteStopTimes(ctx context.Context, tripID int64) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM stop_times WHERE trip_id=$1`, tripID)
	return err
}

func (p *PostgresStore) EnsureStopForTerminal(ctx context.Context, terminalID int64, lat, lon float64, name string) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	if err := p.pool.QueryRow(ctx, `SELECT id FROM stops_canonical WHERE terminal_id=$1 ORDER BY id LIMIT 1`, terminalID).Scan(&id); err == nil {
		return id, nil
	}
	var geomLat, geomLon any
	if lat != 0 || lon != 0 {
		geomLat, geomLon = lat, lon
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO stops_canonical(terminal_id, geom, stop_type) VALUES($1, CASE WHEN $2::double precision IS NOT NULL AND $3::double precision IS NOT NULL THEN ST_SetSRID(ST_MakePoint($3,$2),4326)::geography ELSE NULL END, 'bus') RETURNING id`,
		terminalID, geomLat, geomLon).Scan(&id)
	if err != nil {
		return 0, err
	}
	if name != "" {
		_, _ = p.pool.Exec(ctx, `INSERT INTO stop_names(stop_id, lang, name) VALUES($1,'ru',$2) ON CONFLICT(stop_id, lang) DO NOTHING`, id, name)
	}
	return id, nil
}

func (p *PostgresStore) UpsertTripSource(ctx context.Context, s store.TripSourceRow) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO trip_sources(trip_id, source, observed_at, price, price_currency, schedule_url, duration_s, distance_m, method) VALUES($1,$2,now(),$3,$4,$5,$6,$7,$8) ON CONFLICT(trip_id, source) DO UPDATE SET observed_at=now(), price=EXCLUDED.price, price_currency=EXCLUDED.price_currency, schedule_url=EXCLUDED.schedule_url, duration_s=EXCLUDED.duration_s, distance_m=EXCLUDED.distance_m, method=EXCLUDED.method`,
		s.TripID, s.Source, s.Price, s.PriceCurrency, s.ScheduleURL, s.DurationS, s.DistanceM, s.Method)
	return err
}

func (p *PostgresStore) UpsertStagingTrip(ctx context.Context, s store.StagingTripRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	raw := s.RouteRaw
	if raw == "" {
		raw = "{}"
	}
	matched := s.MatchedStopTimes
	if matched == "" {
		matched = "[]"
	}
	unmatched := s.UnmatchedStops
	if unmatched == "" {
		unmatched = "[]"
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO staging_trips(source, external_route_code, external_trip_code, is_synthetic_key, route_raw, region, transport_type, state, matched_stop_times, unmatched_stops, retry_count, last_attempt_at) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9::jsonb,$10::jsonb,0,now()) ON CONFLICT(source, external_route_code, external_trip_code) DO UPDATE SET is_synthetic_key=EXCLUDED.is_synthetic_key, route_raw=EXCLUDED.route_raw, region=EXCLUDED.region, transport_type=EXCLUDED.transport_type, state=EXCLUDED.state, matched_stop_times=EXCLUDED.matched_stop_times, unmatched_stops=EXCLUDED.unmatched_stops, retry_count=staging_trips.retry_count+1, last_attempt_at=now() RETURNING id`,
		s.Source, s.ExternalRouteCode, s.ExternalTripCode, s.IsSyntheticKey, raw, nullIfEmpty(s.Region).String, nullIfEmpty(s.TransportType).String, s.State, matched, unmatched).Scan(&id)
	if err != nil {
		var existing int64
		if qerr := p.pool.QueryRow(ctx, `SELECT id FROM staging_trips WHERE source=$1 AND external_route_code=$2 AND external_trip_code=$3`, s.Source, s.ExternalRouteCode, s.ExternalTripCode).Scan(&existing); qerr == nil {
			return existing, nil
		}
		return 0, err
	}
	return id, nil
}

func (p *PostgresStore) ListStagingTrips(ctx context.Context, region string, limit int) ([]store.StagingTripRow, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	if limit <= 0 {
		limit = 100
	}
	var out []store.StagingTripRow
	var q string
	var args []any
	if region != "" {
		q = `SELECT id, source, external_route_code, external_trip_code, is_synthetic_key, route_raw::text, coalesce(region,''), coalesce(transport_type,''), state, matched_stop_times::text, unmatched_stops::text, retry_count FROM staging_trips WHERE region=$1 ORDER BY external_route_code, external_trip_code LIMIT $2`
		args = []any{region, limit}
	} else {
		q = `SELECT id, source, external_route_code, external_trip_code, is_synthetic_key, route_raw::text, coalesce(region,''), coalesce(transport_type,''), state, matched_stop_times::text, unmatched_stops::text, retry_count FROM staging_trips ORDER BY external_route_code, external_trip_code LIMIT $1`
		args = []any{limit}
	}
	rs, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var r store.StagingTripRow
		if err := rs.Scan(&r.ID, &r.Source, &r.ExternalRouteCode, &r.ExternalTripCode, &r.IsSyntheticKey, &r.RouteRaw, &r.Region, &r.TransportType, &r.State, &r.MatchedStopTimes, &r.UnmatchedStops, &r.RetryCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func (p *PostgresStore) ListCanonTrips(ctx context.Context, source string) (map[string][]string, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rs, err := p.pool.Query(ctx, `SELECT r.external_route_code, r.external_route_code || '|' || t.external_trip_code FROM trips t JOIN routes r ON r.id=t.route_id WHERE r.source_provider=$1 AND t.provider_id=$1 AND t.valid_to IS NULL AND r.valid_to IS NULL`, source)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string][]string{}
	for rs.Next() {
		var route, trip string
		if err := rs.Scan(&route, &trip); err != nil {
			return nil, err
		}
		out[route] = append(out[route], trip)
	}
	return out, rs.Err()
}

func (p *PostgresStore) AttachTerminalIdentifier(ctx context.Context, terminalID int64, id model.AdaptedIdentifier) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var owner int64
	err := p.pool.QueryRow(ctx, `SELECT terminal_id FROM terminal_identifiers WHERE system=$1 AND code=$2`, id.System, id.Code).Scan(&owner)
	if err == nil {
		if owner == terminalID {
			return 0, applyIdentifierPrimaryRule(ctx, p.pool, terminalID, id.System, id.CodeType, id.Code)
		}
		return owner, nil
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO terminal_identifiers(terminal_id, system, code_type, code, is_primary) VALUES($1,$2,$3,$4,false) ON CONFLICT(terminal_id, system, code_type, code) DO NOTHING`, terminalID, id.System, id.CodeType, id.Code)
	if err != nil {
		var other int64
		if qerr := p.pool.QueryRow(ctx, `SELECT terminal_id FROM terminal_identifiers WHERE system=$1 AND code=$2`, id.System, id.Code).Scan(&other); qerr == nil && other != terminalID {
			return other, nil
		}
		return 0, err
	}
	return 0, applyIdentifierPrimaryRule(ctx, p.pool, terminalID, id.System, id.CodeType, id.Code)
}

func (p *PostgresStore) ListTerminalCodes(ctx context.Context, terminalID int64, system string) ([]model.AdaptedIdentifier, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rs, err := p.pool.Query(ctx, `SELECT system, code_type, code FROM terminal_identifiers WHERE terminal_id=$1 AND ($2='' OR system=$2) ORDER BY system, code_type, code`, terminalID, system)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []model.AdaptedIdentifier
	for rs.Next() {
		var id model.AdaptedIdentifier
		if err := rs.Scan(&id.System, &id.CodeType, &id.Code); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rs.Err()
}

func (p *PostgresStore) DeleteStagingTrip(ctx context.Context, source, routeCode, tripCode string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM staging_trips WHERE source=$1 AND external_route_code=$2 AND external_trip_code=$3`, source, routeCode, tripCode)
	return err
}

func (p *PostgresStore) PublishOutbox(ctx context.Context, aggregate, aggregateID, event, payload string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	if payload == "" {
		payload = "{}"
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO outbox(aggregate, aggregate_id, event, payload) VALUES($1,$2,$3,$4::jsonb)`, aggregate, aggregateID, event, payload)
	return err
}

func (p *PostgresStore) ListOutbox(ctx context.Context, limit int) ([]store.OutboxEvent, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	if limit <= 0 {
		limit = 100
	}
	rs, err := p.pool.Query(ctx, `SELECT id, aggregate, aggregate_id, event, payload::text FROM outbox ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []store.OutboxEvent
	for rs.Next() {
		var e store.OutboxEvent
		if err := rs.Scan(&e.ID, &e.Aggregate, &e.AggregateID, &e.Event, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rs.Err()
}

func (p *PostgresStore) DeleteOutbox(ctx context.Context, id int64) error {
	if p.pool == nil {
		return errNotImplemented
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM outbox WHERE id=$1`, id)
	return err
}

func (t *pgTxStore) FindRouteID(ctx context.Context, source, routeCode string) (int64, bool) {
	var id int64
	if err := t.tx.QueryRow(ctx, `SELECT id FROM routes WHERE source_provider=$1 AND external_route_code=$2`, source, routeCode).Scan(&id); err != nil {
		return 0, false
	}
	return id, true
}

func (t *pgTxStore) TombstoneRoute(ctx context.Context, source, routeCode string) error {
	var id int64
	if err := t.tx.QueryRow(ctx, `UPDATE routes SET valid_to=CURRENT_DATE WHERE source_provider=$1 AND external_route_code=$2 AND valid_to IS NULL RETURNING id`, source, routeCode).Scan(&id); err != nil {
		return nil
	}
	_, _ = t.tx.Exec(ctx, `UPDATE trips SET valid_to=now() WHERE route_id=$1 AND valid_to IS NULL`, id)
	return nil
}

func (t *pgTxStore) FindTrip(ctx context.Context, routeID int64, tripCode string) (store.TripRow, bool) {
	var tr store.TripRow
	var validTo *string
	err := t.tx.QueryRow(ctx, `SELECT id, route_id, provider_id, external_trip_code, coalesce(direction,''), direction_id, coalesce(service_days,''), coalesce(frequency_flag,0), coalesce(period,''), coalesce(service_id,0), duration_s, distance_m, method, valid_to::text FROM trips WHERE route_id=$1 AND external_trip_code=$2`,
		routeID, tripCode).Scan(&tr.ID, &tr.RouteID, &tr.ProviderID, &tr.ExternalTripCode, &tr.Direction, &tr.DirectionID, &tr.ServiceDays, &tr.FrequencyFlag, &tr.Period, &tr.ServiceID, &tr.DurationS, &tr.DistanceM, &tr.Method, &validTo)
	if err != nil {
		return store.TripRow{}, false
	}
	tr.ValidTo = validTo
	return tr, true
}

func (t *pgTxStore) TombstoneTrip(ctx context.Context, tripID int64) error {
	_, err := t.tx.Exec(ctx, `UPDATE trips SET valid_to=now() WHERE id=$1 AND valid_to IS NULL`, tripID)
	return err
}

func (t *pgTxStore) DeleteStopTimes(ctx context.Context, tripID int64) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM stop_times WHERE trip_id=$1`, tripID)
	return err
}

func (t *pgTxStore) EnsureStopForTerminal(ctx context.Context, terminalID int64, lat, lon float64, name string) (int64, error) {
	var id int64
	if err := t.tx.QueryRow(ctx, `SELECT id FROM stops_canonical WHERE terminal_id=$1 ORDER BY id LIMIT 1`, terminalID).Scan(&id); err == nil {
		return id, nil
	}
	var geomLat, geomLon any
	if lat != 0 || lon != 0 {
		geomLat, geomLon = lat, lon
	}
	err := t.tx.QueryRow(ctx, `INSERT INTO stops_canonical(terminal_id, geom, stop_type) VALUES($1, CASE WHEN $2::double precision IS NOT NULL AND $3::double precision IS NOT NULL THEN ST_SetSRID(ST_MakePoint($3,$2),4326)::geography ELSE NULL END, 'bus') RETURNING id`,
		terminalID, geomLat, geomLon).Scan(&id)
	if err != nil {
		return 0, err
	}
	if name != "" {
		_, _ = t.tx.Exec(ctx, `INSERT INTO stop_names(stop_id, lang, name) VALUES($1,'ru',$2) ON CONFLICT(stop_id, lang) DO NOTHING`, id, name)
	}
	return id, nil
}

func (t *pgTxStore) UpsertTripSource(ctx context.Context, s store.TripSourceRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO trip_sources(trip_id, source, observed_at, price, price_currency, schedule_url, duration_s, distance_m, method) VALUES($1,$2,now(),$3,$4,$5,$6,$7,$8) ON CONFLICT(trip_id, source) DO UPDATE SET observed_at=now(), price=EXCLUDED.price, price_currency=EXCLUDED.price_currency, schedule_url=EXCLUDED.schedule_url, duration_s=EXCLUDED.duration_s, distance_m=EXCLUDED.distance_m, method=EXCLUDED.method`,
		s.TripID, s.Source, s.Price, s.PriceCurrency, s.ScheduleURL, s.DurationS, s.DistanceM, s.Method)
	return err
}

func (t *pgTxStore) UpsertStagingTrip(ctx context.Context, s store.StagingTripRow) (int64, error) {
	raw := s.RouteRaw
	if raw == "" {
		raw = "{}"
	}
	matched := s.MatchedStopTimes
	if matched == "" {
		matched = "[]"
	}
	unmatched := s.UnmatchedStops
	if unmatched == "" {
		unmatched = "[]"
	}
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO staging_trips(source, external_route_code, external_trip_code, is_synthetic_key, route_raw, region, transport_type, state, matched_stop_times, unmatched_stops, retry_count, last_attempt_at) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9::jsonb,$10::jsonb,0,now()) ON CONFLICT(source, external_route_code, external_trip_code) DO UPDATE SET is_synthetic_key=EXCLUDED.is_synthetic_key, route_raw=EXCLUDED.route_raw, region=EXCLUDED.region, transport_type=EXCLUDED.transport_type, state=EXCLUDED.state, matched_stop_times=EXCLUDED.matched_stop_times, unmatched_stops=EXCLUDED.unmatched_stops, retry_count=staging_trips.retry_count+1, last_attempt_at=now() RETURNING id`,
		s.Source, s.ExternalRouteCode, s.ExternalTripCode, s.IsSyntheticKey, raw, nullIfEmpty(s.Region).String, nullIfEmpty(s.TransportType).String, s.State, matched, unmatched).Scan(&id)
	if err != nil {
		if qerr := t.tx.QueryRow(ctx, `SELECT id FROM staging_trips WHERE source=$1 AND external_route_code=$2 AND external_trip_code=$3`, s.Source, s.ExternalRouteCode, s.ExternalTripCode).Scan(&id); qerr == nil {
			return id, nil
		}
		return 0, err
	}
	return id, nil
}

func (t *pgTxStore) ListStagingTrips(ctx context.Context, region string, limit int) ([]store.StagingTripRow, error) {
	return t.parent.ListStagingTrips(ctx, region, limit)
}

func (t *pgTxStore) ListCanonTrips(ctx context.Context, source string) (map[string][]string, error) {
	return t.parent.ListCanonTrips(ctx, source)
}

func (t *pgTxStore) ListTerminalCodes(ctx context.Context, terminalID int64, system string) ([]model.AdaptedIdentifier, error) {
	rs, err := t.tx.Query(ctx, `SELECT system, code_type, code FROM terminal_identifiers WHERE terminal_id=$1 AND ($2='' OR system=$2) ORDER BY system, code_type, code`, terminalID, system)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []model.AdaptedIdentifier
	for rs.Next() {
		var id model.AdaptedIdentifier
		if err := rs.Scan(&id.System, &id.CodeType, &id.Code); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rs.Err()
}

func (t *pgTxStore) AttachTerminalIdentifier(ctx context.Context, terminalID int64, id model.AdaptedIdentifier) (int64, error) {
	var owner int64
	err := t.tx.QueryRow(ctx, `SELECT terminal_id FROM terminal_identifiers WHERE system=$1 AND code=$2`, id.System, id.Code).Scan(&owner)
	if err == nil {
		if owner == terminalID {
			return 0, applyIdentifierPrimaryRule(ctx, t.tx, terminalID, id.System, id.CodeType, id.Code)
		}
		return owner, nil
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO terminal_identifiers(terminal_id, system, code_type, code, is_primary) VALUES($1,$2,$3,$4,false) ON CONFLICT(terminal_id, system, code_type, code) DO NOTHING`, terminalID, id.System, id.CodeType, id.Code)
	if err != nil {
		var other int64
		if qerr := t.tx.QueryRow(ctx, `SELECT terminal_id FROM terminal_identifiers WHERE system=$1 AND code=$2`, id.System, id.Code).Scan(&other); qerr == nil && other != terminalID {
			return other, nil
		}
		return 0, err
	}
	return 0, applyIdentifierPrimaryRule(ctx, t.tx, terminalID, id.System, id.CodeType, id.Code)
}

func (t *pgTxStore) DeleteStagingTrip(ctx context.Context, source, routeCode, tripCode string) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM staging_trips WHERE source=$1 AND external_route_code=$2 AND external_trip_code=$3`, source, routeCode, tripCode)
	return err
}

func (t *pgTxStore) PublishOutbox(ctx context.Context, aggregate, aggregateID, event, payload string) error {
	if payload == "" {
		payload = "{}"
	}
	_, err := t.tx.Exec(ctx, `INSERT INTO outbox(aggregate, aggregate_id, event, payload) VALUES($1,$2,$3,$4::jsonb)`, aggregate, aggregateID, event, payload)
	return err
}

func (t *pgTxStore) ListOutbox(ctx context.Context, limit int) ([]store.OutboxEvent, error) {
	return t.parent.ListOutbox(ctx, limit)
}

func (t *pgTxStore) DeleteOutbox(ctx context.Context, id int64) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM outbox WHERE id=$1`, id)
	return err
}

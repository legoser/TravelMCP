package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

type StopRow = store.StopRow
type CarrierRow = store.CarrierRow
type RouteRow = store.RouteRow
type TripRow = store.TripRow
type FrequencyRow = store.FrequencyRow
type StopTimeRow = store.StopTimeRow
type TransferRow = store.TransferRow
type QualityRow = store.QualityRow
type ServiceRow = store.ServiceRow
type ServiceDayRow = store.ServiceDayRow
type ServiceExceptionRow = store.ServiceExceptionRow
type FareRow = store.FareRow
type FareAttributeRow = store.FareAttributeRow
type FareRuleRow = store.FareRuleRow
type ZoneRow = store.ZoneRow
type StopZoneRow = store.StopZoneRow
type ImportRow = store.ImportRow
type UserRow = store.UserRow
type ApiKeyRow = store.ApiKeyRow
type PlaceRow = store.PlaceRow
type TerminalRow = store.TerminalRow

type PostgresStore struct {
	pool *pgxpool.Pool
	dsn  string
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	slog.Info("postgres store connected", "dsn", dsn)
	return &PostgresStore{pool: pool, dsn: dsn}, nil
}

func (p *PostgresStore) Migrate(ctx context.Context) error {
	if p.pool == nil {
		slog.Warn("postgres Migrate: pool nil", "dsn", p.dsn)
		return nil
	}
	matches, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	if len(matches) == 0 {
		slog.Warn("postgres Migrate: no migration files found", "dir", "migrations")
		return nil
	}
	sort.Strings(matches)
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}
		if len(data) == 0 {
			slog.Warn("postgres Migrate: empty file", "path", path)
			continue
		}
		slog.Info("postgres Migrate: applying", "path", path, "size", len(data))
		if _, err := p.pool.Exec(ctx, string(data)); err != nil {
			slog.Error("postgres migrate failed", "path", path, "err", err)
			return fmt.Errorf("migrate %s: %w", path, err)
		}
		slog.Info("postgres Migrate: applied", "path", path)
	}
	return nil
}

func (p *PostgresStore) Close() error {
	if p.pool == nil {
		return nil
	}
	p.pool.Close()
	return nil
}

func (p *PostgresStore) WithTx(ctx context.Context, fn func(store.Store) error) error {
	if p.pool == nil {
		return fn(p)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ps := &pgTxStore{tx: tx, parent: p}
	if err := fn(ps); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PostgresStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	if s.TerminalID == 0 {
		return 0, fmt.Errorf("UpsertStop: terminal_id required")
	}
	if s.ID != 0 {
		err := p.pool.QueryRow(ctx, `INSERT INTO stops_canonical(id, terminal_id, geom, stop_type) VALUES($1,$2, ST_SetSRID(ST_MakePoint($3,$4),4326)::geography, $5) ON CONFLICT(id) DO UPDATE SET terminal_id=EXCLUDED.terminal_id, geom=EXCLUDED.geom RETURNING id`, s.ID, s.TerminalID, s.Lon, s.Lat, s.StopType).Scan(&id)
		if err == nil {
			if s.Name != "" {
				_, _ = p.pool.Exec(ctx, `INSERT INTO stop_names(stop_id, lang, name) VALUES($1,'ru',$2) ON CONFLICT(stop_id, lang) DO UPDATE SET name=EXCLUDED.name`, s.ID, s.Name)
			}
			return s.ID, nil
		}
		return 0, err
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO stops_canonical(terminal_id, geom, stop_type) VALUES($1, ST_SetSRID(ST_MakePoint($2,$3),4326)::geography, $4) RETURNING id`, s.TerminalID, s.Lon, s.Lat, s.StopType).Scan(&id)
	if err == nil && s.Name != "" {
		_, _ = p.pool.Exec(ctx, `INSERT INTO stop_names(stop_id, lang, name) VALUES($1,'ru',$2) ON CONFLICT(stop_id, lang) DO UPDATE SET name=EXCLUDED.name`, id, s.Name)
	}
	return id, err
}

func (p *PostgresStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	if c.INN != "" {
		err := p.pool.QueryRow(ctx, `INSERT INTO carriers(inn, name_ru, address, iata, icao, sirena) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(inn) WHERE inn IS NOT NULL DO UPDATE SET name_ru=EXCLUDED.name_ru, address=EXCLUDED.address RETURNING id`, c.INN, c.Name, c.Address, c.IATA, c.ICAO, c.Sirena).Scan(&id)
		if err == nil {
			_, _ = p.pool.Exec(ctx, `INSERT INTO carrier_identifiers(carrier_id, system, code_type, code) VALUES($1,'mintrans','inn',$2) ON CONFLICT DO NOTHING`, id, c.INN)
			if c.Code != "" && c.Code != c.INN {
				_, _ = p.pool.Exec(ctx, `INSERT INTO carrier_identifiers(carrier_id, system, code_type, code) VALUES($1,'mintrans','code',$2) ON CONFLICT DO NOTHING`, id, c.Code)
			}
			return id, nil
		}
		_ = p.pool.QueryRow(ctx, `SELECT id FROM carriers WHERE inn=$1`, c.INN).Scan(&id)
		return id, err
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO carriers(name_ru, address, iata, icao, sirena) VALUES($1,$2,$3,$4,$5) RETURNING id`, c.Name, c.Address, c.IATA, c.ICAO, c.Sirena).Scan(&id)
	if err != nil {
		_ = p.pool.QueryRow(ctx, `SELECT id FROM carriers WHERE name_ru=$1 AND address=$2`, c.Name, c.Address).Scan(&id)
	}
	if c.Code != "" {
		_, _ = p.pool.Exec(ctx, `INSERT INTO carrier_identifiers(carrier_id, system, code_type, code) VALUES($1,'mintrans','code',$2) ON CONFLICT DO NOTHING`, id, c.Code)
	}
	return id, err
}
func (p *PostgresStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	src := r.ProviderID
	if src == "" {
		src = "mintrans"
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO routes(carrier_id, external_code, short_name, long_name, mode, external_uid, ord, source_provider) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(source_provider, external_code) DO UPDATE SET long_name=EXCLUDED.long_name, short_name=EXCLUDED.short_name, carrier_id=EXCLUDED.carrier_id RETURNING id`, r.CarrierID, r.ExternalCode, r.ShortName, r.LongName, r.Mode, r.ExternalUID, r.Ord, src).Scan(&id)
	return id, err
}
func (p *PostgresStore) UpsertRouteRegion(ctx context.Context, routeID int64, region string) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO route_regions(route_id, region_code) VALUES($1,$2) ON CONFLICT DO NOTHING`, routeID, region)
	return err
}
func (p *PostgresStore) UpsertTrip(ctx context.Context, t TripRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO trips(route_id, provider_id, direction, service_days, frequency_flag, period, service_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, t.RouteID, t.ProviderID, t.Direction, t.ServiceDays, t.FrequencyFlag, t.Period, t.ServiceID).Scan(&id)
	return id, err
}
func (p *PostgresStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO frequencies(trip_id, start_time, end_time, headway_secs, exact_times) VALUES($1,$2,$3,$4,$5) ON CONFLICT(trip_id, start_time) DO NOTHING`, f.TripID, f.StartMin, f.EndMin, f.HeadwayMin, f.ExactTimes)
	return err
}
func (p *PostgresStore) UpsertStopTime(ctx context.Context, st StopTimeRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, pickup_type, drop_off_type, dwell) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(trip_id, seq) DO UPDATE SET stop_id=EXCLUDED.stop_id, arrival=EXCLUDED.arrival, departure=EXCLUDED.departure`, st.TripID, st.StopID, st.Seq, st.Arrival, st.Departure, st.PickupType, st.DropOffType, st.Dwell)
	return err
}
func (p *PostgresStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO transfers(from_stop_id, to_stop_id, minutes, min_transfer_time, distance_m, within_station) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(from_stop_id, to_stop_id) DO UPDATE SET minutes=EXCLUDED.minutes`, tr.FromStopID, tr.ToStopID, tr.Minutes, tr.MinTransferTime, tr.DistanceM, tr.WithinStation)
	return err
}
func (p *PostgresStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }
func (p *PostgresStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, code, msg, at) VALUES($1,$2,$3,$4,$5,$6,to_timestamp($7))`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Code, q.Msg, q.At)
	return err
}
func (p *PostgresStore) ClearQualityIssues(ctx context.Context, providerID string) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM quality_issues WHERE provider_id=$1`, providerID)
	return err
}
func (p *PostgresStore) ClearProviderData(ctx context.Context, providerID string) error {
	if p.pool == nil {
		return nil
	}
	_, _ = p.pool.Exec(ctx, `DELETE FROM stop_times WHERE trip_id IN (SELECT id FROM trips WHERE provider_id=$1)`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM transfers WHERE from_stop_id IN (SELECT id FROM stops_canonical WHERE terminal_id IN (SELECT id FROM terminals WHERE id IN (SELECT terminal_id FROM stops_canonical)))`)
	_, _ = p.pool.Exec(ctx, `DELETE FROM trips WHERE provider_id=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM routes WHERE source_provider=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM services WHERE provider_id=$1`, providerID)
	return nil
}
func (p *PostgresStore) UpsertService(ctx context.Context, s ServiceRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO services(id, provider_id, name, start_date, end_date) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name, start_date=EXCLUDED.start_date, end_date=EXCLUDED.end_date`, s.ID, s.ProviderID, s.Name, s.StartDate, s.EndDate)
	return err
}
func (p *PostgresStore) UpsertServiceDay(ctx context.Context, d ServiceDayRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO service_days(service_id, weekday) VALUES($1,$2) ON CONFLICT(service_id, weekday) DO NOTHING`, d.ServiceID, d.Weekday)
	return err
}
func (p *PostgresStore) UpsertServiceException(ctx context.Context, e ServiceExceptionRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO service_exceptions(service_id, date, exception_type) VALUES($1,$2,$3) ON CONFLICT(service_id, date) DO UPDATE SET exception_type=EXCLUDED.exception_type`, e.ServiceID, e.Date, e.ExceptionType)
	return err
}
func (p *PostgresStore) UpsertPlace(ctx context.Context, r PlaceRow, names map[string]string) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	if r.ID != 0 {
		err := p.pool.QueryRow(ctx, `INSERT INTO places(id, parent_id, admin_level, level, geom, tz, valid_from, valid_to) VALUES($1,$2,$3,$4, CASE WHEN $5::double precision IS NOT NULL AND $6::double precision IS NOT NULL THEN ST_SetSRID(ST_MakePoint($6,$5),4326)::geography ELSE NULL END, $7, $8, $9) ON CONFLICT(id) DO UPDATE SET parent_id=EXCLUDED.parent_id, admin_level=EXCLUDED.admin_level, level=EXCLUDED.level, geom=EXCLUDED.geom, tz=EXCLUDED.tz, valid_to=EXCLUDED.valid_to RETURNING id`, r.ID, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		err := p.pool.QueryRow(ctx, `INSERT INTO places(parent_id, admin_level, level, geom, tz, valid_from, valid_to) VALUES($1,$2,$3, CASE WHEN $4::double precision IS NOT NULL AND $5::double precision IS NOT NULL THEN ST_SetSRID(ST_MakePoint($5,$4),4326)::geography ELSE NULL END, $6, $7, $8) RETURNING id`, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo).Scan(&id)
		if err != nil {
			return 0, err
		}
	}
	for lang, name := range names {
		norm := strings.ToLower(strings.TrimSpace(name))
		norm = strings.Join(strings.Fields(norm), " ")
		if _, err := p.pool.Exec(ctx, `INSERT INTO place_names(place_id, lang, name, normalized) VALUES($1,$2,$3,$4) ON CONFLICT(place_id, lang) DO UPDATE SET name=EXCLUDED.name, normalized=EXCLUDED.normalized`, id, lang, name, norm); err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (p *PostgresStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	if r.ID != 0 {
		var locked bool
		_ = p.pool.QueryRow(ctx, `SELECT is_locked FROM terminals WHERE id=$1`, r.ID).Scan(&locked)
		if locked {
			_ = p.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: r.ID, Reason: "conflicts_with_confirmed", Score: 0})
			return r.ID, nil
		}
	}
	var id int64
	if r.ID != 0 {
		err := p.pool.QueryRow(ctx, `INSERT INTO terminals(id, place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at, is_locked) VALUES($1,$2,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography,$5, daterange($6::date, $7::date, '[]'), $8, $9, $10, $11, $12) ON CONFLICT(id) DO UPDATE SET place_id=EXCLUDED.place_id, geom=EXCLUDED.geom, tz=EXCLUDED.tz, is_locked=terminals.is_locked RETURNING id`, r.ID, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt, r.IsLocked).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		err := p.pool.QueryRow(ctx, `INSERT INTO terminals(place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at, is_locked) VALUES($1,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,$4, daterange($5::date, $6::date, '[]'), $7, $8, $9, $10, $11) RETURNING id`, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt, r.IsLocked).Scan(&id)
		if err != nil {
			return 0, err
		}
	}
	for lang, name := range names {
		if _, err := p.pool.Exec(ctx, `INSERT INTO terminal_names(terminal_id, lang, name, is_primary) VALUES($1,$2,$3,true) ON CONFLICT(terminal_id, lang) DO UPDATE SET name=EXCLUDED.name`, id, lang, name); err != nil {
			return 0, err
		}
	}
	for _, ident := range identifiers {
		if _, err := p.pool.Exec(ctx, `INSERT INTO terminal_identifiers(terminal_id, system, code_type, code, is_primary) VALUES($1,$2,$3,$4,false) ON CONFLICT(terminal_id, system, code_type) DO UPDATE SET code=EXCLUDED.code`, id, ident.System, ident.CodeType, ident.Code); err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (p *PostgresStore) GetTerminal(ctx context.Context, id int64) (map[string]any, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	var name string
	var lat, lon float64
	var locked bool
	var placeID *int64
	err := p.pool.QueryRow(ctx, `SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.is_locked, t.place_id FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru' WHERE t.id=$1`, id).Scan(&id, &name, &lat, &lon, &locked, &placeID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": name, "lat": lat, "lon": lon, "is_locked": locked, "place_id": placeID}, nil
}
func (p *PostgresStore) GetTerminalTags(ctx context.Context, id int64) (map[string]string, error) {
	out := map[string]string{}
	if p.pool == nil {
		return out, nil
	}
	var raw []byte
	if err := p.pool.QueryRow(ctx, `SELECT coalesce(tags,'{}') FROM terminal_tags WHERE terminal_id=$1`, id).Scan(&raw); err != nil {
		return out, nil
	}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}
func (p *PostgresStore) SetTerminalTag(ctx context.Context, id int64, key, value string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	if value == "" {
		_, err := p.pool.Exec(ctx, `UPDATE terminal_tags SET tags = tags - $2 WHERE terminal_id=$1`, id, key)
		return err
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO terminal_tags(terminal_id, tags) VALUES($1, jsonb_build_object($2::text,$3::text)) ON CONFLICT(terminal_id) DO UPDATE SET tags = terminal_tags.tags || EXCLUDED.tags`, id, key, value)
	return err
}
func (p *PostgresStore) DeleteReviewQueue(ctx context.Context, entityType string, entityID int64, reason string) error {
	if p.pool == nil {
		return nil
	}
	if reason == "" {
		_, err := p.pool.Exec(ctx, `DELETE FROM review_queue WHERE entity_type=$1 AND entity_id=$2`, entityType, entityID)
		return err
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM review_queue WHERE entity_type=$1 AND entity_id=$2 AND reason=$3`, entityType, entityID, reason)
	return err
}
func (p *PostgresStore) SaveProvenance(ctx context.Context, pr model.Provenance) error {
	if p.pool == nil {
		return nil
	}
	raw := ""
	if len(pr.Raw) > 0 {
		raw = string(pr.Raw)
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, raw, actor_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(entity_type, entity_id, source) DO UPDATE SET confidence=EXCLUDED.confidence, observed_at=EXCLUDED.observed_at, raw=EXCLUDED.raw`, pr.EntityType, pr.EntityID, pr.Source, pr.Confidence, pr.ObservedAt, raw, pr.ActorID)
	return err
}
func (p *PostgresStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO review_queue(entity_type, entity_id, reason, score) VALUES($1,$2,$3,$4) ON CONFLICT(entity_type, entity_id, reason) DO UPDATE SET score=EXCLUDED.score`, e.EntityType, e.EntityID, e.Reason, e.Score)
	return err
}
func (p *PostgresStore) ListReviewQueue(ctx context.Context, limit int) ([]store.ReviewQueueRow, error) {
	if p.pool == nil {
		return []store.ReviewQueueRow{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `SELECT entity_type, entity_id, reason, score, extract(epoch from created_at)::bigint FROM review_queue ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ReviewQueueRow
	for rows.Next() {
		var r store.ReviewQueueRow
		if err := rows.Scan(&r.EntityType, &r.EntityID, &r.Reason, &r.Score, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (p *PostgresStore) ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error) {
	return p.ListTerminalsFiltered(ctx, limit, offset, sort, "asc", "")
}

func (p *PostgresStore) ListTerminalsFiltered(ctx context.Context, limit, offset int, sort, order, q string) ([]map[string]any, int, error) {
	if p.pool == nil {
		return []map[string]any{}, 0, nil
	}
	dir := "ASC"
	if strings.ToLower(order) == "desc" {
		dir = "DESC"
	}
	orderClause := "t.id " + dir
	if sort == "name" {
		orderClause = "tn.name " + dir + ", t.id " + dir
	} else if sort == "is_locked" {
		orderClause = "t.is_locked " + dir + ", t.id " + dir
	}
	q = strings.TrimSpace(q)
	hasQ := q != ""
	var total int
	if hasQ {
		_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM terminals t WHERE EXISTS (SELECT 1 FROM terminal_names tns WHERE tns.terminal_id=t.id AND tns.name ILIKE '%' || $1 || '%')`, q).Scan(&total)
	} else {
		_ = p.pool.QueryRow(ctx, `SELECT count(*) FROM terminals`).Scan(&total)
	}
	var rows pgx.Rows
	var err error
	if hasQ {
		rows, err = p.pool.Query(ctx, fmt.Sprintf(`SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.is_locked, t.place_id FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru' WHERE EXISTS (SELECT 1 FROM terminal_names tns WHERE tns.terminal_id=t.id AND tns.name ILIKE '%%' || $3 || '%%') ORDER BY %s LIMIT $1 OFFSET $2`, orderClause), limit, offset, q)
	} else {
		rows, err = p.pool.Query(ctx, fmt.Sprintf(`SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.is_locked, t.place_id FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru' ORDER BY %s LIMIT $1 OFFSET $2`, orderClause), limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var name string
		var lat, lon float64
		var locked bool
		var placeID *int64
		_ = rows.Scan(&id, &name, &lat, &lon, &locked, &placeID)
		out = append(out, map[string]any{"id": id, "name": name, "lat": lat, "lon": lon, "is_locked": locked, "place_id": placeID})
	}
	return out, total, nil
}
func (p *PostgresStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	if p.pool == nil {
		return 0, "", errNotImplemented
	}
	var id int64
	var tz *string
	err := p.pool.QueryRow(ctx, `SELECT p.id, p.tz FROM place_closure pc JOIN places p ON p.id=pc.ancestor_id WHERE pc.descendant_id=$1 AND p.level=4 LIMIT 1`, placeID).Scan(&id, &tz)
	if err != nil {
		return 0, "", err
	}
	if tz == nil {
		return id, "", nil
	}
	return id, *tz, nil
}
func (p *PostgresStore) LogImportEntry(ctx context.Context, jobID int64, entityType, entityID, stage, action string, confidence float64, distanceM int, lev float64, source string) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO import_logs(job_id, entity_type, entity_id, stage, action, confidence, distance_m, lev, source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		jobID, entityType, entityID, stage, action, confidence, distanceM, lev, source)
	return err
}
func (p *PostgresStore) ImportAdaptedRecords(ctx context.Context, records []model.AdaptedRecord) (int, int, error) {
	return 0, 0, errNotImplemented
}
func (p *PostgresStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	allow := map[string]bool{}
	for _, pr := range providers {
		allow[pr] = true
	}
	net := model.NewNetwork()
	stopIDMap := map[int64]string{}
	// canonical stops: stops_canonical + stop_names + terminals geom
	srows, err := p.pool.Query(ctx, `SELECT sc.id, sc.terminal_id, ST_Y(sc.geom::geometry) as lat, ST_X(sc.geom::geometry) as lon, sc.stop_type, coalesce(sn.name, tname.name, 'stop') as name FROM stops_canonical sc LEFT JOIN stop_names sn ON sn.stop_id=sc.id AND sn.lang='ru' LEFT JOIN terminal_names tname ON tname.terminal_id=sc.terminal_id AND tname.lang='ru'`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var id, terminalID int64
		var lat, lon sql.NullFloat64
		var stopType, name sql.NullString
		_ = srows.Scan(&id, &terminalID, &lat, &lon, &stopType, &name)
		la, lo := 0.0, 0.0
		if lat.Valid {
			la = lat.Float64
		}
		if lon.Valid {
			lo = lon.Float64
		}
		if !lat.Valid || !lon.Valid {
			_ = p.pool.QueryRow(ctx, `SELECT ST_Y(geom::geometry), ST_X(geom::geometry) FROM terminals WHERE id=$1`, terminalID).Scan(&la, &lo)
		}
		code := fmt.Sprintf("%d", id)
		net.Stops[code] = &model.Stop{ID: code, ProviderID: "mintrans", Name: name.String, Lat: la, Lon: lo, Type: model.StopType(stopType.String)}
		stopIDMap[id] = code
	}
	routeRows, err := p.pool.Query(ctx, `SELECT id, source_provider, carrier_id, external_code, short_name, long_name, mode FROM routes`)
	if err != nil {
		return nil, err
	}
	defer routeRows.Close()
	routeIDToCode := map[int64]string{}
	routeIDToMode := map[int64]string{}
	for routeRows.Next() {
		var r RouteRow
		_ = routeRows.Scan(&r.ID, &r.ProviderID, &r.CarrierID, &r.ExternalCode, &r.ShortName, &r.LongName, &r.Mode)
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		routeIDToCode[r.ID] = r.ExternalCode
		routeIDToMode[r.ID] = r.Mode
		net.Routes[r.ExternalCode] = &model.Route{ID: r.ExternalCode, ProviderID: r.ProviderID, ShortName: r.ShortName, LongName: r.LongName, Mode: model.Mode(r.Mode)}
	}
	tRows, err := p.pool.Query(ctx, `SELECT id, route_id, provider_id, direction, service_id FROM trips`)
	if err != nil {
		return nil, err
	}
	defer tRows.Close()
	tripByID := map[int64]TripRow{}
	var trips []TripRow
	for tRows.Next() {
		var r TripRow
		_ = tRows.Scan(&r.ID, &r.RouteID, &r.ProviderID, &r.Direction, &r.ServiceID)
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		trips = append(trips, r)
		tripByID[r.ID] = r
	}
	stRows, err := p.pool.Query(ctx, `SELECT trip_id, stop_id, seq, arrival, departure FROM stop_times ORDER BY trip_id, seq`)
	if err == nil {
		defer stRows.Close()
		grouped := map[int64][]model.StopTime{}
		for stRows.Next() {
			var tripID, stopID int64
			var seq, arr, dep int
			_ = stRows.Scan(&tripID, &stopID, &seq, &arr, &dep)
			sid, ok := stopIDMap[stopID]
			if !ok {
				continue
			}
			if _, ok := tripByID[tripID]; !ok {
				continue
			}
			grouped[tripID] = append(grouped[tripID], model.StopTime{StopID: sid, Sequence: seq, ArrivalSec: arr, DepartureSec: dep})
		}
		for _, t := range trips {
			times := grouped[t.ID]
			if len(times) == 0 {
				continue
			}
			code := routeIDToCode[t.RouteID]
			mode := routeIDToMode[t.RouteID]
			mt := &model.Trip{ID: code + ":" + t.Direction, RouteID: code, ProviderID: t.ProviderID, Mode: model.Mode(mode), ServiceID: t.ServiceID, StopTimes: times}
			net.Trips[mt.ID] = mt
		}
	}
	trRows, err := p.pool.Query(ctx, `SELECT from_stop_id, to_stop_id, minutes FROM transfers`)
	if err == nil {
		defer trRows.Close()
		for trRows.Next() {
			var fid, tid int64
			var minutes int
			_ = trRows.Scan(&fid, &tid, &minutes)
			from, ok1 := stopIDMap[fid]
			to, ok2 := stopIDMap[tid]
			if !ok1 || !ok2 {
				continue
			}
			net.Transfers = append(net.Transfers, model.Transfer{FromStopID: from, ToStopID: to, Minutes: minutes})
		}
	}
	if faRows, err := p.pool.Query(ctx, `SELECT fare_id, price, currency, basis FROM fare_attributes`); err == nil {
		defer faRows.Close()
		for faRows.Next() {
			var fid string
			var price float64
			var cur, basis string
			_ = faRows.Scan(&fid, &price, &cur, &basis)
			net.FareAttributes[fid] = &model.FareAttribute{FareID: fid, Price: price, Currency: cur, Basis: basis}
		}
	}
	if zRows, err := p.pool.Query(ctx, `SELECT zone_id, name_ru, name_en FROM zones`); err == nil {
		defer zRows.Close()
		for zRows.Next() {
			var zid, nru, nen string
			_ = zRows.Scan(&zid, &nru, &nen)
			net.Zones[zid] = &model.Zone{ID: zid, NameRu: nru, NameEn: nen}
		}
	}
	routeIDToFare := map[int64]string{}
	if frRows, err := p.pool.Query(ctx, `SELECT fare_id, route_id, origin_zone, destination_zone FROM fare_rules`); err == nil {
		defer frRows.Close()
		for frRows.Next() {
			var fid string
			var rid int64
			var o, d *string
			_ = frRows.Scan(&fid, &rid, &o, &d)
			code := routeIDToCode[rid]
			net.FareRules = append(net.FareRules, model.FareRule{FareID: fid, RouteID: code, OriginZone: o, DestinationZone: d})
			routeIDToFare[rid] = fid
		}
	}
	if szRows, err := p.pool.Query(ctx, `SELECT stop_id, zone_id FROM stop_zones`); err == nil {
		defer szRows.Close()
		for szRows.Next() {
			var sid int64
			var zid string
			_ = szRows.Scan(&sid, &zid)
			if code, ok := stopIDMap[sid]; ok {
				net.StopZones[code] = zid
			}
		}
	}
	dayBase := time.Now().UTC().Truncate(24 * time.Hour)
	if !day.IsZero() {
		dayBase = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	}
	for _, trip := range net.Trips {
		for i := 0; i < len(trip.StopTimes)-1; i++ {
			from := trip.StopTimes[i]
			to := trip.StopTimes[i+1]
			dep := dayBase.Add(time.Duration(from.DepartureSec) * time.Second)
			arr := dayBase.Add(time.Duration(to.ArrivalSec) * time.Second)
			if arr.Before(dep) {
				arr = arr.Add(24 * time.Hour)
			}
			net.Connections = append(net.Connections, model.Connection{TripID: trip.ID, ProviderID: trip.ProviderID, RouteID: trip.RouteID, Mode: trip.Mode, From: from.StopID, To: to.StopID, Departure: dep, Arrival: arr})
		}
	}
	net.BuildIndexes()
	return net, nil
}
func (p *PostgresStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status, checksum) VALUES($1,$2,$3,$4, 'legacy') ON CONFLICT(provider_id, checksum) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records`, providerID, at, records, "ok")
	return err
}
func (p *PostgresStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status, snapshot, checksum, issues) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(provider_id, checksum) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records, snapshot=EXCLUDED.snapshot, issues=EXCLUDED.issues`, providerID, at, records, "ok", snapshot, checksum, issues)
	return err
}
func (p *PostgresStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	if p.pool == nil {
		return ImportRow{}, false
	}
	var r ImportRow
	err := p.pool.QueryRow(ctx, `SELECT provider_id, extract(epoch from at)::bigint, records, status, snapshot, checksum, issues FROM imports WHERE provider_id=$1 ORDER BY at DESC LIMIT 1`, providerID).Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues)
	if err != nil {
		return ImportRow{}, false
	}
	return r, true
}
func (p *PostgresStore) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	return UserRow{}, false
}
func (p *PostgresStore) GetUserByID(ctx context.Context, id int64) (UserRow, bool) {
	return UserRow{}, false
}
func (p *PostgresStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	return nil, errNotImplemented
}
func (p *PostgresStore) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	return errNotImplemented
}
func (p *PostgresStore) UpdateUserRole(ctx context.Context, id int64, role string) error {
	return errNotImplemented
}
func (p *PostgresStore) UpdateUserConfig(ctx context.Context, id int64, config string) error {
	return errNotImplemented
}
func (p *PostgresStore) DeleteUser(ctx context.Context, id int64) error { return errNotImplemented }
func (p *PostgresStore) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	return ApiKeyRow{}, errNotImplemented
}
func (p *PostgresStore) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) {
	return ApiKeyRow{}, false
}
func (p *PostgresStore) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	return nil, errNotImplemented
}
func (p *PostgresStore) DeleteApiKey(ctx context.Context, id int64, userID int64) error {
	return errNotImplemented
}
func (p *PostgresStore) TouchApiKey(ctx context.Context, key string) error { return nil }

func (p *PostgresStore) TryConsumeQuota(ctx context.Context, provider string, limit int) (bool, int, error) {
	if p.pool == nil {
		return true, 1, nil
	}
	var used int
	err := p.pool.QueryRow(ctx, `INSERT INTO api_quotas(provider, day, used, limit, reset_at) VALUES($1, CURRENT_DATE, 1, $2, (CURRENT_DATE + INTERVAL '1 day')::timestamptz AT TIME ZONE 'Europe/Moscow') ON CONFLICT (provider, day) DO UPDATE SET used = api_quotas.used + 1 WHERE api_quotas.used < api_quotas.limit RETURNING used`, provider, limit).Scan(&used)
	if err != nil {
		return false, 0, nil
	}
	_, _ = p.pool.Exec(ctx, `INSERT INTO api_calls(provider, endpoint, at, cost) VALUES($1,'quota_consume', now(), 1)`, provider)
	return true, used, nil
}

func (p *PostgresStore) GetQuota(ctx context.Context, provider string, day time.Time) (store.QuotaRow, bool) {
	if p.pool == nil {
		return store.QuotaRow{}, false
	}
	d := day
	if d.IsZero() {
		d = time.Now()
	}
	var r store.QuotaRow
	var resetAt *string
	err := p.pool.QueryRow(ctx, `SELECT provider, day::text, used, limit, reset_at::text FROM api_quotas WHERE provider=$1 AND day=$2::date`, provider, d.Format("2006-01-02")).Scan(&r.Provider, &r.Day, &r.Used, &r.Limit, &resetAt)
	if err != nil {
		return store.QuotaRow{}, false
	}
	r.ResetAt = resetAt
	return r, true
}

func (p *PostgresStore) SetQuotaLimit(ctx context.Context, provider string, limit int) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO api_quotas(provider, day, used, limit, reset_at) VALUES($1, CURRENT_DATE, 0, $2, (CURRENT_DATE + INTERVAL '1 day')::timestamptz AT TIME ZONE 'Europe/Moscow') ON CONFLICT (provider, day) DO UPDATE SET limit=EXCLUDED.limit`, provider, limit)
	return err
}

func (p *PostgresStore) RecordApiCall(ctx context.Context, provider, endpoint string, cost int) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO api_calls(provider, endpoint, at, cost) VALUES($1,$2, now(), $3)`, provider, endpoint, cost)
	return err
}

func (p *PostgresStore) ListQuotas(ctx context.Context) ([]store.QuotaRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT provider, day::text, used, limit, reset_at::text FROM api_quotas ORDER BY day DESC, provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.QuotaRow
	for rows.Next() {
		var r store.QuotaRow
		var resetAt *string
		if err := rows.Scan(&r.Provider, &r.Day, &r.Used, &r.Limit, &resetAt); err != nil {
			return nil, err
		}
		r.ResetAt = resetAt
		out = append(out, r)
	}
	return out, nil
}

func (p *PostgresStore) WriteAuditLog(ctx context.Context, userID *int64, action, entityType string, entityID *int64, details string) error {
	if p.pool == nil {
		return nil
	}
	if details == "" {
		details = "{}"
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO audit_log(user_id, action, entity_type, entity_id, details) VALUES($1,$2,$3,$4,$5::jsonb)`, userID, action, entityType, entityID, details)
	return err
}

func (p *PostgresStore) ListAuditLogs(ctx context.Context, limit int) ([]store.AuditLogRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `SELECT id, user_id, action, entity_type, entity_id, extract(epoch from at)::bigint, details::text FROM audit_log ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AuditLogRow
	for rows.Next() {
		var r store.AuditLogRow
		var details *string
		if err := rows.Scan(&r.ID, &r.UserID, &r.Action, &r.EntityType, &r.EntityID, &r.At, &details); err != nil {
			return nil, err
		}
		if details != nil {
			r.Details = *details
		}
		out = append(out, r)
	}
	return out, nil
}

func (p *PostgresStore) ListImports(ctx context.Context, limit int) ([]store.ImportRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := p.pool.Query(ctx, `SELECT provider_id, extract(epoch from at)::bigint, records, status, snapshot, checksum, issues FROM imports ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ImportRow
	for rows.Next() {
		var r store.ImportRow
		if err := rows.Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (p *PostgresStore) ListImportLogs(ctx context.Context, limit int) ([]store.ImportLogRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `SELECT id, job_id, entity_type, entity_id, stage, action, confidence, distance_m, lev, source, extract(epoch from at)::bigint FROM import_logs ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ImportLogRow
	for rows.Next() {
		var r store.ImportLogRow
		if err := rows.Scan(&r.ID, &r.JobID, &r.EntityType, &r.EntityID, &r.Stage, &r.Action, &r.Confidence, &r.DistanceM, &r.Lev, &r.Source, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (p *PostgresStore) EnqueueJob(ctx context.Context, j store.JobRow) (int64, error) {
	if p.pool == nil {
		return 0, fmt.Errorf("pool nil")
	}
	var id int64
	payload := j.Payload
	if payload == "" {
		payload = "{}"
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO jobs(type, payload, region, state, next_run) VALUES($1,$2::jsonb,$3,'pending', now()) RETURNING id`, j.Type, payload, j.Region).Scan(&id)
	return id, err
}

func (p *PostgresStore) ClaimNextJob(ctx context.Context) (*store.JobRow, error) {
	if p.pool == nil {
		return nil, fmt.Errorf("pool nil")
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var r store.JobRow
	var payload string
	var nextRun time.Time
	var createdAt time.Time
	err = tx.QueryRow(ctx, `SELECT id, type, payload::text, coalesce(region,''), state, attempts, next_run, coalesce(last_error,''), created_at FROM jobs WHERE state IN ('pending','retry') AND next_run <= now() ORDER BY next_run LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&r.ID, &r.Type, &payload, &r.Region, &r.State, &r.Attempts, &nextRun, &r.LastError, &createdAt)
	if err != nil {
		return nil, err
	}
	r.Payload = payload
	r.NextRun = nextRun.Format(time.RFC3339)
	r.CreatedAt = createdAt.Unix()
	if _, err := tx.Exec(ctx, `UPDATE jobs SET state='running', attempts=attempts+1, updated_at=now() WHERE id=$1`, r.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	r.State = "running"
	return &r, nil
}

func (p *PostgresStore) MarkJobDone(ctx context.Context, id int64) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `UPDATE jobs SET state='done', updated_at=now() WHERE id=$1`, id)
	return err
}

func (p *PostgresStore) MarkJobRetry(ctx context.Context, id int64, errMsg string) error {
	if p.pool == nil {
		return nil
	}
	var attempts int
	_ = p.pool.QueryRow(ctx, `SELECT attempts FROM jobs WHERE id=$1`, id).Scan(&attempts)
	backoff := time.Duration((attempts+1)*2) * time.Minute
	if backoff > 30*time.Minute {
		backoff = 30 * time.Minute
	}
	_, err := p.pool.Exec(ctx, `UPDATE jobs SET state='retry', last_error=$2, attempts=attempts+1, next_run=now()+$3::interval, updated_at=now() WHERE id=$1`, id, errMsg, fmt.Sprintf("%d seconds", int(backoff.Seconds())))
	return err
}

func (p *PostgresStore) MarkJobDead(ctx context.Context, id int64, errMsg string) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `UPDATE jobs SET state='dead', last_error=$2, updated_at=now() WHERE id=$1`, id, errMsg)
	return err
}

func (p *PostgresStore) ListJobs(ctx context.Context, limit int) ([]store.JobRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := p.pool.Query(ctx, `SELECT id, type, payload::text, coalesce(region,''), state, attempts, next_run::text, coalesce(last_error,''), extract(epoch from created_at)::bigint FROM jobs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.JobRow
	for rows.Next() {
		var r store.JobRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Payload, &r.Region, &r.State, &r.Attempts, &r.NextRun, &r.LastError, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type pgTxStore struct {
	tx     pgx.Tx
	parent *PostgresStore
}

func (t *pgTxStore) Migrate(ctx context.Context) error                            { return nil }
func (t *pgTxStore) Close() error                                                 { return nil }
func (t *pgTxStore) WithTx(ctx context.Context, fn func(store.Store) error) error { return fn(t) }

func (t *pgTxStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	if s.TerminalID == 0 {
		return 0, fmt.Errorf("UpsertStop: terminal_id required")
	}
	var id int64
	if s.ID != 0 {
		err := t.tx.QueryRow(ctx, `INSERT INTO stops_canonical(id, terminal_id, geom, stop_type) VALUES($1,$2, ST_SetSRID(ST_MakePoint($3,$4),4326)::geography, $5) ON CONFLICT(id) DO UPDATE SET terminal_id=EXCLUDED.terminal_id, geom=EXCLUDED.geom RETURNING id`, s.ID, s.TerminalID, s.Lon, s.Lat, s.StopType).Scan(&id)
		if err == nil {
			if s.Name != "" {
				_, _ = t.tx.Exec(ctx, `INSERT INTO stop_names(stop_id, lang, name) VALUES($1,'ru',$2) ON CONFLICT(stop_id, lang) DO UPDATE SET name=EXCLUDED.name`, s.ID, s.Name)
			}
			return s.ID, nil
		}
		return 0, err
	}
	err := t.tx.QueryRow(ctx, `INSERT INTO stops_canonical(terminal_id, geom, stop_type) VALUES($1, ST_SetSRID(ST_MakePoint($2,$3),4326)::geography, $4) RETURNING id`, s.TerminalID, s.Lon, s.Lat, s.StopType).Scan(&id)
	if err == nil && s.Name != "" {
		_, _ = t.tx.Exec(ctx, `INSERT INTO stop_names(stop_id, lang, name) VALUES($1,'ru',$2) ON CONFLICT(stop_id, lang) DO UPDATE SET name=EXCLUDED.name`, id, s.Name)
	}
	return id, err
}

func (t *pgTxStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	var id int64
	if c.INN != "" {
		err := t.tx.QueryRow(ctx, `INSERT INTO carriers(inn, name_ru, address, iata, icao, sirena) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(inn) WHERE inn IS NOT NULL DO UPDATE SET name_ru=EXCLUDED.name_ru, address=EXCLUDED.address RETURNING id`, c.INN, c.Name, c.Address, c.IATA, c.ICAO, c.Sirena).Scan(&id)
		if err == nil {
			_, _ = t.tx.Exec(ctx, `INSERT INTO carrier_identifiers(carrier_id, system, code_type, code) VALUES($1,'mintrans','inn',$2) ON CONFLICT DO NOTHING`, id, c.INN)
			return id, nil
		}
		_ = t.tx.QueryRow(ctx, `SELECT id FROM carriers WHERE inn=$1`, c.INN).Scan(&id)
		return id, err
	}
	err := t.tx.QueryRow(ctx, `INSERT INTO carriers(name_ru, address, iata, icao, sirena) VALUES($1,$2,$3,$4,$5) RETURNING id`, c.Name, c.Address, c.IATA, c.ICAO, c.Sirena).Scan(&id)
	if c.Code != "" {
		_, _ = t.tx.Exec(ctx, `INSERT INTO carrier_identifiers(carrier_id, system, code_type, code) VALUES($1,'mintrans','code',$2) ON CONFLICT DO NOTHING`, id, c.Code)
	}
	return id, err
}
func (t *pgTxStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	var id int64
	src := r.ProviderID
	if src == "" {
		src = "mintrans"
	}
	err := t.tx.QueryRow(ctx, `INSERT INTO routes(carrier_id, external_code, short_name, long_name, mode, external_uid, ord, source_provider) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(source_provider, external_code) DO UPDATE SET long_name=EXCLUDED.long_name, short_name=EXCLUDED.short_name, carrier_id=EXCLUDED.carrier_id RETURNING id`, r.CarrierID, r.ExternalCode, r.ShortName, r.LongName, r.Mode, r.ExternalUID, r.Ord, src).Scan(&id)
	return id, err
}
func (t *pgTxStore) UpsertRouteRegion(ctx context.Context, routeID int64, region string) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO route_regions(route_id, region_code) VALUES($1,$2) ON CONFLICT DO NOTHING`, routeID, region)
	return err
}
func (t *pgTxStore) UpsertTrip(ctx context.Context, r TripRow) (int64, error) {
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO trips(route_id, provider_id, direction, service_days, frequency_flag, period, service_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, r.RouteID, r.ProviderID, r.Direction, r.ServiceDays, r.FrequencyFlag, r.Period, r.ServiceID).Scan(&id)
	return id, err
}
func (t *pgTxStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO frequencies(trip_id, start_time, end_time, headway_secs, exact_times) VALUES($1,$2,$3,$4,$5) ON CONFLICT(trip_id, start_time) DO NOTHING`, f.TripID, f.StartMin, f.EndMin, f.HeadwayMin, f.ExactTimes)
	return err
}
func (t *pgTxStore) UpsertStopTime(ctx context.Context, st StopTimeRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, pickup_type, drop_off_type, dwell) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(trip_id, seq) DO UPDATE SET stop_id=EXCLUDED.stop_id, arrival=EXCLUDED.arrival`, st.TripID, st.StopID, st.Seq, st.Arrival, st.Departure, st.PickupType, st.DropOffType, st.Dwell)
	return err
}
func (t *pgTxStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO transfers(from_stop_id, to_stop_id, minutes, min_transfer_time, distance_m, within_station) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(from_stop_id, to_stop_id) DO UPDATE SET minutes=EXCLUDED.minutes`, tr.FromStopID, tr.ToStopID, tr.Minutes, tr.MinTransferTime, tr.DistanceM, tr.WithinStation)
	return err
}
func (t *pgTxStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }
func (t *pgTxStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, code, msg, at) VALUES($1,$2,$3,$4,$5,$6,to_timestamp($7))`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Code, q.Msg, q.At)
	return err
}
func (t *pgTxStore) ClearQualityIssues(ctx context.Context, providerID string) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM quality_issues WHERE provider_id=$1`, providerID)
	return err
}
func (t *pgTxStore) ClearProviderData(ctx context.Context, providerID string) error {
	_, _ = t.tx.Exec(ctx, `DELETE FROM stop_times WHERE trip_id IN (SELECT id FROM trips WHERE provider_id=$1)`, providerID)
	_, _ = t.tx.Exec(ctx, `DELETE FROM trips WHERE provider_id=$1`, providerID)
	_, _ = t.tx.Exec(ctx, `DELETE FROM routes WHERE source_provider=$1`, providerID)
	_, _ = t.tx.Exec(ctx, `DELETE FROM services WHERE provider_id=$1`, providerID)
	return nil
}
func (t *pgTxStore) UpsertService(ctx context.Context, s ServiceRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO services(id, provider_id, name, start_date, end_date) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, s.ID, s.ProviderID, s.Name, s.StartDate, s.EndDate)
	return err
}
func (t *pgTxStore) UpsertServiceDay(ctx context.Context, d ServiceDayRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO service_days(service_id, weekday) VALUES($1,$2) ON CONFLICT DO NOTHING`, d.ServiceID, d.Weekday)
	return err
}
func (t *pgTxStore) UpsertServiceException(ctx context.Context, e ServiceExceptionRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO service_exceptions(service_id, date, exception_type) VALUES($1,$2,$3) ON CONFLICT(service_id, date) DO UPDATE SET exception_type=EXCLUDED.exception_type`, e.ServiceID, e.Date, e.ExceptionType)
	return err
}
func (t *pgTxStore) UpsertPlace(ctx context.Context, r PlaceRow, names map[string]string) (int64, error) {
	var id int64
	if r.ID != 0 {
		err := t.tx.QueryRow(ctx, `INSERT INTO places(id, parent_id, admin_level, level, geom, tz, valid_from, valid_to) VALUES($1,$2,$3,$4, CASE WHEN $5::double precision IS NOT NULL AND $6::double precision IS NOT NULL THEN ST_SetSRID(ST_MakePoint($6,$5),4326)::geography ELSE NULL END, $7, $8, $9) ON CONFLICT(id) DO UPDATE SET parent_id=EXCLUDED.parent_id, admin_level=EXCLUDED.admin_level, level=EXCLUDED.level, geom=EXCLUDED.geom, tz=EXCLUDED.tz, valid_to=EXCLUDED.valid_to RETURNING id`, r.ID, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		err := t.tx.QueryRow(ctx, `INSERT INTO places(parent_id, admin_level, level, geom, tz, valid_from, valid_to) VALUES($1,$2,$3, CASE WHEN $4::double precision IS NOT NULL AND $5::double precision IS NOT NULL THEN ST_SetSRID(ST_MakePoint($5,$4),4326)::geography ELSE NULL END, $6, $7, $8) RETURNING id`, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo).Scan(&id)
		if err != nil {
			return 0, err
		}
	}
	for lang, name := range names {
		norm := strings.ToLower(strings.TrimSpace(name))
		norm = strings.Join(strings.Fields(norm), " ")
		if _, err := t.tx.Exec(ctx, `INSERT INTO place_names(place_id, lang, name, normalized) VALUES($1,$2,$3,$4) ON CONFLICT(place_id, lang) DO UPDATE SET name=EXCLUDED.name, normalized=EXCLUDED.normalized`, id, lang, name, norm); err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (t *pgTxStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	if r.ID != 0 {
		var locked bool
		_ = t.tx.QueryRow(ctx, `SELECT is_locked FROM terminals WHERE id=$1`, r.ID).Scan(&locked)
		if locked {
			_ = t.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: r.ID, Reason: "conflicts_with_confirmed"})
			return r.ID, nil
		}
	}
	var id int64
	if r.ID != 0 {
		err := t.tx.QueryRow(ctx, `INSERT INTO terminals(id, place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at, is_locked) VALUES($1,$2,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography,$5, daterange($6::date, $7::date, '[]'), $8, $9, $10, $11, $12) ON CONFLICT(id) DO UPDATE SET place_id=EXCLUDED.place_id, geom=EXCLUDED.geom, tz=EXCLUDED.tz, is_locked=terminals.is_locked RETURNING id`, r.ID, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt, r.IsLocked).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		err := t.tx.QueryRow(ctx, `INSERT INTO terminals(place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at, is_locked) VALUES($1,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,$4, daterange($5::date, $6::date, '[]'), $7, $8, $9, $10, $11) RETURNING id`, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt, r.IsLocked).Scan(&id)
		if err != nil {
			return 0, err
		}
	}
	for lang, name := range names {
		if _, err := t.tx.Exec(ctx, `INSERT INTO terminal_names(terminal_id, lang, name, is_primary) VALUES($1,$2,$3,true) ON CONFLICT(terminal_id, lang) DO UPDATE SET name=EXCLUDED.name`, id, lang, name); err != nil {
			return 0, err
		}
	}
	for _, ident := range identifiers {
		if _, err := t.tx.Exec(ctx, `INSERT INTO terminal_identifiers(terminal_id, system, code_type, code, is_primary) VALUES($1,$2,$3,$4,false) ON CONFLICT(terminal_id, system, code_type) DO UPDATE SET code=EXCLUDED.code`, id, ident.System, ident.CodeType, ident.Code); err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (t *pgTxStore) SaveProvenance(ctx context.Context, p model.Provenance) error {
	raw := ""
	if len(p.Raw) > 0 {
		raw = string(p.Raw)
	}
	_, err := t.tx.Exec(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, raw, actor_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(entity_type, entity_id, source) DO UPDATE SET confidence=EXCLUDED.confidence`, p.EntityType, p.EntityID, p.Source, p.Confidence, p.ObservedAt, raw, p.ActorID)
	return err
}
func (t *pgTxStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO review_queue(entity_type, entity_id, reason, score) VALUES($1,$2,$3,$4) ON CONFLICT(entity_type, entity_id, reason) DO UPDATE SET score=EXCLUDED.score`, e.EntityType, e.EntityID, e.Reason, e.Score)
	return err
}
func (t *pgTxStore) ListReviewQueue(ctx context.Context, limit int) ([]store.ReviewQueueRow, error) {
	return t.parent.ListReviewQueue(ctx, limit)
}
func (t *pgTxStore) DeleteReviewQueue(ctx context.Context, entityType string, entityID int64, reason string) error {
	return t.parent.DeleteReviewQueue(ctx, entityType, entityID, reason)
}
func (t *pgTxStore) GetTerminal(ctx context.Context, id int64) (map[string]any, error) {
	return t.parent.GetTerminal(ctx, id)
}
func (t *pgTxStore) GetTerminalTags(ctx context.Context, id int64) (map[string]string, error) {
	return t.parent.GetTerminalTags(ctx, id)
}
func (t *pgTxStore) SetTerminalTag(ctx context.Context, id int64, key, value string) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO terminal_tags(terminal_id, tags) VALUES($1, jsonb_build_object($2::text,$3::text)) ON CONFLICT(terminal_id) DO UPDATE SET tags = terminal_tags.tags || EXCLUDED.tags`, id, key, value)
	return err
}

func (t *pgTxStore) ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error) {
	return t.ListTerminalsFiltered(ctx, limit, offset, sort, "asc", "")
}

func (t *pgTxStore) ListTerminalsFiltered(ctx context.Context, limit, offset int, sort, order, q string) ([]map[string]any, int, error) {
	dir := "ASC"
	if strings.ToLower(order) == "desc" {
		dir = "DESC"
	}
	orderClause := "t.id " + dir
	if sort == "name" {
		orderClause = "tn.name " + dir + ", t.id " + dir
	} else if sort == "is_locked" {
		orderClause = "t.is_locked " + dir + ", t.id " + dir
	}
	q = strings.TrimSpace(q)
	hasQ := q != ""
	var total int
	if hasQ {
		_ = t.tx.QueryRow(ctx, `SELECT count(*) FROM terminals t WHERE EXISTS (SELECT 1 FROM terminal_names tns WHERE tns.terminal_id=t.id AND tns.name ILIKE '%' || $1 || '%')`, q).Scan(&total)
	} else {
		_ = t.tx.QueryRow(ctx, `SELECT count(*) FROM terminals`).Scan(&total)
	}
	var rows pgx.Rows
	var err error
	if hasQ {
		rows, err = t.tx.Query(ctx, fmt.Sprintf(`SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.is_locked, t.place_id FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru' WHERE EXISTS (SELECT 1 FROM terminal_names tns WHERE tns.terminal_id=t.id AND tns.name ILIKE '%%' || $3 || '%%') ORDER BY %s LIMIT $1 OFFSET $2`, orderClause), limit, offset, q)
	} else {
		rows, err = t.tx.Query(ctx, fmt.Sprintf(`SELECT t.id, coalesce(tn.name,''), ST_Y(t.geom::geometry), ST_X(t.geom::geometry), t.is_locked, t.place_id FROM terminals t LEFT JOIN terminal_names tn ON tn.terminal_id=t.id AND tn.lang='ru' ORDER BY %s LIMIT $1 OFFSET $2`, orderClause), limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var name string
		var lat, lon float64
		var locked bool
		var placeID *int64
		_ = rows.Scan(&id, &name, &lat, &lon, &locked, &placeID)
		out = append(out, map[string]any{"id": id, "name": name, "lat": lat, "lon": lon, "is_locked": locked, "place_id": placeID})
	}
	return out, total, nil
}
func (t *pgTxStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	var id int64
	var tz *string
	err := t.tx.QueryRow(ctx, `SELECT p.id, p.tz FROM place_closure pc JOIN places p ON p.id=pc.ancestor_id WHERE pc.descendant_id=$1 AND p.level=4 LIMIT 1`, placeID).Scan(&id, &tz)
	if err != nil {
		return 0, "", err
	}
	if tz == nil {
		return id, "", nil
	}
	return id, *tz, nil
}
func (t *pgTxStore) ImportAdaptedRecords(ctx context.Context, records []model.AdaptedRecord) (int, int, error) {
	return 0, 0, errNotImplemented
}
func (t *pgTxStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	return t.parent.LoadNetwork(ctx, providers, day)
}
func (t *pgTxStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status, checksum) VALUES($1,$2,$3,$4, 'legacy') ON CONFLICT(provider_id, checksum) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records`, providerID, at, records, "ok")
	return err
}
func (t *pgTxStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status, snapshot, checksum, issues) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(provider_id, checksum) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records, snapshot=EXCLUDED.snapshot, issues=EXCLUDED.issues`, providerID, at, records, "ok", snapshot, checksum, issues)
	return err
}
func (t *pgTxStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	var r ImportRow
	err := t.tx.QueryRow(ctx, `SELECT provider_id, extract(epoch from at)::bigint, records, status, snapshot, checksum, issues FROM imports WHERE provider_id=$1 ORDER BY at DESC LIMIT 1`, providerID).Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues)
	if err != nil {
		return ImportRow{}, false
	}
	return r, true
}
func (t *pgTxStore) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	return UserRow{}, false
}
func (t *pgTxStore) GetUserByID(ctx context.Context, id int64) (UserRow, bool) {
	return UserRow{}, false
}
func (t *pgTxStore) ListUsers(ctx context.Context) ([]UserRow, error) { return nil, errNotImplemented }
func (t *pgTxStore) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	return errNotImplemented
}
func (t *pgTxStore) UpdateUserRole(ctx context.Context, id int64, role string) error {
	return errNotImplemented
}
func (t *pgTxStore) UpdateUserConfig(ctx context.Context, id int64, config string) error {
	return errNotImplemented
}
func (t *pgTxStore) DeleteUser(ctx context.Context, id int64) error { return errNotImplemented }
func (t *pgTxStore) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	return ApiKeyRow{}, errNotImplemented
}
func (t *pgTxStore) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) {
	return ApiKeyRow{}, false
}
func (t *pgTxStore) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	return nil, errNotImplemented
}
func (t *pgTxStore) DeleteApiKey(ctx context.Context, id int64, userID int64) error {
	return errNotImplemented
}
func (t *pgTxStore) TouchApiKey(ctx context.Context, key string) error { return nil }

func (t *pgTxStore) TryConsumeQuota(ctx context.Context, provider string, limit int) (bool, int, error) {
	var used int
	err := t.tx.QueryRow(ctx, `INSERT INTO api_quotas(provider, day, used, limit, reset_at) VALUES($1, CURRENT_DATE, 1, $2, (CURRENT_DATE + INTERVAL '1 day')::timestamptz AT TIME ZONE 'Europe/Moscow') ON CONFLICT (provider, day) DO UPDATE SET used = api_quotas.used + 1 WHERE api_quotas.used < api_quotas.limit RETURNING used`, provider, limit).Scan(&used)
	if err != nil {
		return false, 0, nil
	}
	_, _ = t.tx.Exec(ctx, `INSERT INTO api_calls(provider, endpoint, at, cost) VALUES($1,'quota_consume', now(), 1)`, provider)
	return true, used, nil
}

func (t *pgTxStore) GetQuota(ctx context.Context, provider string, day time.Time) (store.QuotaRow, bool) {
	d := day
	if d.IsZero() {
		d = time.Now()
	}
	var r store.QuotaRow
	var resetAt *string
	err := t.tx.QueryRow(ctx, `SELECT provider, day::text, used, limit, reset_at::text FROM api_quotas WHERE provider=$1 AND day=$2::date`, provider, d.Format("2006-01-02")).Scan(&r.Provider, &r.Day, &r.Used, &r.Limit, &resetAt)
	if err != nil {
		return store.QuotaRow{}, false
	}
	r.ResetAt = resetAt
	return r, true
}

func (t *pgTxStore) SetQuotaLimit(ctx context.Context, provider string, limit int) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO api_quotas(provider, day, used, limit, reset_at) VALUES($1, CURRENT_DATE, 0, $2, (CURRENT_DATE + INTERVAL '1 day')::timestamptz AT TIME ZONE 'Europe/Moscow') ON CONFLICT (provider, day) DO UPDATE SET limit=EXCLUDED.limit`, provider, limit)
	return err
}

func (t *pgTxStore) RecordApiCall(ctx context.Context, provider, endpoint string, cost int) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO api_calls(provider, endpoint, at, cost) VALUES($1,$2, now(), $3)`, provider, endpoint, cost)
	return err
}

func (t *pgTxStore) ListQuotas(ctx context.Context) ([]store.QuotaRow, error) {
	rows, err := t.tx.Query(ctx, `SELECT provider, day::text, used, limit, reset_at::text FROM api_quotas ORDER BY day DESC, provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.QuotaRow
	for rows.Next() {
		var r store.QuotaRow
		var resetAt *string
		if err := rows.Scan(&r.Provider, &r.Day, &r.Used, &r.Limit, &resetAt); err != nil {
			return nil, err
		}
		r.ResetAt = resetAt
		out = append(out, r)
	}
	return out, nil
}

func (t *pgTxStore) WriteAuditLog(ctx context.Context, userID *int64, action, entityType string, entityID *int64, details string) error {
	if details == "" {
		details = "{}"
	}
	_, err := t.tx.Exec(ctx, `INSERT INTO audit_log(user_id, action, entity_type, entity_id, details) VALUES($1,$2,$3,$4,$5::jsonb)`, userID, action, entityType, entityID, details)
	return err
}

func (t *pgTxStore) ListAuditLogs(ctx context.Context, limit int) ([]store.AuditLogRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := t.tx.Query(ctx, `SELECT id, user_id, action, entity_type, entity_id, extract(epoch from at)::bigint, details::text FROM audit_log ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.AuditLogRow
	for rows.Next() {
		var r store.AuditLogRow
		var details *string
		if err := rows.Scan(&r.ID, &r.UserID, &r.Action, &r.EntityType, &r.EntityID, &r.At, &details); err != nil {
			return nil, err
		}
		if details != nil {
			r.Details = *details
		}
		out = append(out, r)
	}
	return out, nil
}

func (t *pgTxStore) ListImports(ctx context.Context, limit int) ([]store.ImportRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := t.tx.Query(ctx, `SELECT provider_id, extract(epoch from at)::bigint, records, status, snapshot, checksum, issues FROM imports ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ImportRow
	for rows.Next() {
		var r store.ImportRow
		if err := rows.Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (t *pgTxStore) ListImportLogs(ctx context.Context, limit int) ([]store.ImportLogRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := t.tx.Query(ctx, `SELECT id, job_id, entity_type, entity_id, stage, action, confidence, distance_m, lev, source, extract(epoch from at)::bigint FROM import_logs ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ImportLogRow
	for rows.Next() {
		var r store.ImportLogRow
		if err := rows.Scan(&r.ID, &r.JobID, &r.EntityType, &r.EntityID, &r.Stage, &r.Action, &r.Confidence, &r.DistanceM, &r.Lev, &r.Source, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (t *pgTxStore) EnqueueJob(ctx context.Context, j store.JobRow) (int64, error) {
	var id int64
	payload := j.Payload
	if payload == "" {
		payload = "{}"
	}
	err := t.tx.QueryRow(ctx, `INSERT INTO jobs(type, payload, region, state, next_run) VALUES($1,$2::jsonb,$3,'pending', now()) RETURNING id`, j.Type, payload, j.Region).Scan(&id)
	return id, err
}

func (t *pgTxStore) ClaimNextJob(ctx context.Context) (*store.JobRow, error) {
	var r store.JobRow
	var payload string
	var nextRun time.Time
	var createdAt time.Time
	err := t.tx.QueryRow(ctx, `SELECT id, type, payload::text, coalesce(region,''), state, attempts, next_run, coalesce(last_error,''), created_at FROM jobs WHERE state IN ('pending','retry') AND next_run <= now() ORDER BY next_run LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&r.ID, &r.Type, &payload, &r.Region, &r.State, &r.Attempts, &nextRun, &r.LastError, &createdAt)
	if err != nil {
		return nil, err
	}
	r.Payload = payload
	r.NextRun = nextRun.Format(time.RFC3339)
	r.CreatedAt = createdAt.Unix()
	if _, err := t.tx.Exec(ctx, `UPDATE jobs SET state='running', attempts=attempts+1, updated_at=now() WHERE id=$1`, r.ID); err != nil {
		return nil, err
	}
	r.State = "running"
	return &r, nil
}

func (t *pgTxStore) MarkJobDone(ctx context.Context, id int64) error {
	_, err := t.tx.Exec(ctx, `UPDATE jobs SET state='done', updated_at=now() WHERE id=$1`, id)
	return err
}

func (t *pgTxStore) MarkJobRetry(ctx context.Context, id int64, errMsg string) error {
	var attempts int
	_ = t.tx.QueryRow(ctx, `SELECT attempts FROM jobs WHERE id=$1`, id).Scan(&attempts)
	backoff := time.Duration((attempts+1)*2) * time.Minute
	if backoff > 30*time.Minute {
		backoff = 30 * time.Minute
	}
	_, err := t.tx.Exec(ctx, `UPDATE jobs SET state='retry', last_error=$2, attempts=attempts+1, next_run=now()+$3::interval, updated_at=now() WHERE id=$1`, id, errMsg, fmt.Sprintf("%d seconds", int(backoff.Seconds())))
	return err
}

func (t *pgTxStore) MarkJobDead(ctx context.Context, id int64, errMsg string) error {
	_, err := t.tx.Exec(ctx, `UPDATE jobs SET state='dead', last_error=$2, updated_at=now() WHERE id=$1`, id, errMsg)
	return err
}

func (t *pgTxStore) ListJobs(ctx context.Context, limit int) ([]store.JobRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := t.tx.Query(ctx, `SELECT id, type, payload::text, coalesce(region,''), state, attempts, next_run::text, coalesce(last_error,''), extract(epoch from created_at)::bigint FROM jobs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.JobRow
	for rows.Next() {
		var r store.JobRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Payload, &r.Region, &r.State, &r.Attempts, &r.NextRun, &r.LastError, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func init() {
	store.Register("postgres", func(ctx context.Context, dsn string) (store.Store, error) {
		return NewPostgresStore(ctx, dsn)
	})
}

var errNotImplemented = errStr("postgres store: метод ещё не реализован")

type errStr string

func (e errStr) Error() string { return string(e) }

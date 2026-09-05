package postgres

import (
	"context"
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

type CityRow = store.CityRow
type StationRow = store.StationRow
type StopRow = store.StopRow
type StationCodeRow = store.StationCodeRow
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

func (p *PostgresStore) UpsertCity(ctx context.Context, c CityRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO cities(name, region_code, lat, lon, timezone, population, kind, source) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(name, region_code) DO UPDATE SET lat=EXCLUDED.lat, lon=EXCLUDED.lon, timezone=EXCLUDED.timezone, kind=EXCLUDED.kind RETURNING id`, c.Name, c.RegionCode, c.Lat, c.Lon, c.Timezone, c.Population, c.Kind, c.Source).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}
func (p *PostgresStore) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	if s.ID != 0 {
		var id int64
		err := p.pool.QueryRow(ctx, `INSERT INTO stations(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name, lat=EXCLUDED.lat, lon=EXCLUDED.lon, geo_cell=EXCLUDED.geo_cell, region_code=EXCLUDED.region_code, quality_flags=EXCLUDED.quality_flags, city_id=EXCLUDED.city_id RETURNING id`, s.ID, s.Name, s.Lat, s.Lon, s.GeoCell, s.RegionCode, s.Timezone, s.QualityFlags, s.PrimaryProvider, s.CityID).Scan(&id)
		return s.ID, err
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO stations(name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(name, region_code) DO UPDATE SET lat=EXCLUDED.lat, lon=EXCLUDED.lon, geo_cell=EXCLUDED.geo_cell, quality_flags=EXCLUDED.quality_flags, city_id=EXCLUDED.city_id RETURNING id`, s.Name, s.Lat, s.Lon, s.GeoCell, s.RegionCode, s.Timezone, s.QualityFlags, s.PrimaryProvider, s.CityID).Scan(&id)
	return id, err
}
func (p *PostgresStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	if s.ID != 0 {
		err := p.pool.QueryRow(ctx, `INSERT INTO stops(id, station_id, provider_id, external_code, stop_type, transport_type, name, raw_name) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(provider_id, external_code) DO UPDATE SET station_id=EXCLUDED.station_id, name=EXCLUDED.name RETURNING id`, s.ID, s.StationID, s.ProviderID, s.ExternalCode, s.StopType, s.TransportType, s.Name, s.RawName).Scan(&id)
		if err == nil {
			return s.ID, nil
		}
		return 0, err
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO stops(station_id, provider_id, external_code, stop_type, transport_type, name, raw_name) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(provider_id, external_code) DO UPDATE SET station_id=EXCLUDED.station_id, name=EXCLUDED.name RETURNING id`, s.StationID, s.ProviderID, s.ExternalCode, s.StopType, s.TransportType, s.Name, s.RawName).Scan(&id)
	return id, err
}
func (p *PostgresStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO station_codes(station_id, provider_id, code_type, code, name_form, address) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(station_id, provider_id, code_type) DO UPDATE SET code=EXCLUDED.code`, c.StationID, c.ProviderID, c.CodeType, c.Code, c.NameForm, c.Address)
	return err
}
func (p *PostgresStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO carriers(provider_id, name, code, inn, address, iata, icao, sirena) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(provider_id, code) DO UPDATE SET name=EXCLUDED.name, inn=EXCLUDED.inn, address=EXCLUDED.address RETURNING id`, c.ProviderID, c.Name, c.Code, c.INN, c.Address, c.IATA, c.ICAO, c.Sirena).Scan(&id)
	if err != nil {
		// try fallback select
		_ = p.pool.QueryRow(ctx, `SELECT id FROM carriers WHERE provider_id=$1 AND code=$2`, c.ProviderID, c.Code).Scan(&id)
	}
	return id, err
}
func (p *PostgresStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	if p.pool == nil {
		return 0, errNotImplemented
	}
	var id int64
	err := p.pool.QueryRow(ctx, `INSERT INTO routes(provider_id, carrier_id, external_code, short_name, long_name, mode, external_uid, ord, source_provider) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$1) ON CONFLICT(provider_id, external_code) DO UPDATE SET long_name=EXCLUDED.long_name, short_name=EXCLUDED.short_name RETURNING id`, r.ProviderID, r.CarrierID, r.ExternalCode, r.ShortName, r.LongName, r.Mode, r.ExternalUID, r.Ord).Scan(&id)
	return id, err
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
	_, err := p.pool.Exec(ctx, `INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, pickup_type, drop_off_type, dwell) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(trip_id, stop_id, seq) DO UPDATE SET arrival=EXCLUDED.arrival, departure=EXCLUDED.departure`, st.TripID, st.StopID, st.Seq, st.Arrival, st.Departure, st.PickupType, st.DropOffType, st.Dwell)
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
	_, err := p.pool.Exec(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, code, msg, at) VALUES($1,$2,$3,$4,$5,$6,$7)`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Code, q.Msg, q.At)
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
	_, _ = p.pool.Exec(ctx, `DELETE FROM transfers WHERE from_stop_id IN (SELECT id FROM stops WHERE provider_id=$1) OR to_stop_id IN (SELECT id FROM stops WHERE provider_id=$1)`, providerID, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM stops WHERE provider_id=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM trips WHERE provider_id=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM routes WHERE provider_id=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM carriers WHERE provider_id=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM services WHERE provider_id=$1`, providerID)
	_, _ = p.pool.Exec(ctx, `DELETE FROM stations WHERE primary_provider=$1`, providerID)
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
	var id int64
	if r.ID != 0 {
		err := p.pool.QueryRow(ctx, `INSERT INTO terminals(id, place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES($1,$2,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography,$5, daterange($6::date, $7::date, '[]'), $8, $9, $10, $11) ON CONFLICT(id) DO UPDATE SET place_id=EXCLUDED.place_id, geom=EXCLUDED.geom, tz=EXCLUDED.tz RETURNING id`, r.ID, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		err := p.pool.QueryRow(ctx, `INSERT INTO terminals(place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES($1,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,$4, daterange($5::date, $6::date, '[]'), $7, $8, $9, $10) RETURNING id`, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt).Scan(&id)
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
	_, err := p.pool.Exec(ctx, `INSERT INTO review_queue(entity_type, entity_id, reason, score) VALUES($1,$2,$3,$4) ON CONFLICT(entity_type, entity_id) DO UPDATE SET reason=EXCLUDED.reason, score=EXCLUDED.score`, e.EntityType, e.EntityID, e.Reason, e.Score)
	return err
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
	stations := map[int64]StationRow{}
	rows, err := p.pool.Query(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r StationRow
		_ = rows.Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
		stations[r.ID] = r
	}
	stopIDMap := map[int64]string{}
	srows, err := p.pool.Query(ctx, `SELECT id, station_id, provider_id, external_code, stop_type, transport_type, name, raw_name FROM stops`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var r StopRow
		_ = srows.Scan(&r.ID, &r.StationID, &r.ProviderID, &r.ExternalCode, &r.StopType, &r.TransportType, &r.Name, &r.RawName)
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		st, ok := stations[r.StationID]
		lat, lon := 0.0, 0.0
		if ok {
			lat, lon = st.Lat, st.Lon
		}
		net.Stops[r.ExternalCode] = &model.Stop{ID: r.ExternalCode, ProviderID: r.ProviderID, Name: r.Name, Lat: lat, Lon: lon, Type: model.StopType(r.StopType)}
		stopIDMap[r.ID] = r.ExternalCode
	}
	routeRows, err := p.pool.Query(ctx, `SELECT id, provider_id, carrier_id, external_code, short_name, long_name, mode FROM routes`)
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
	_, err := p.pool.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status) VALUES($1,$2,$3,$4) ON CONFLICT(provider_id) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records`, providerID, at.Unix(), records, "ok")
	return err
}
func (p *PostgresStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status, snapshot, checksum, issues) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(provider_id) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records, snapshot=EXCLUDED.snapshot, checksum=EXCLUDED.checksum, issues=EXCLUDED.issues`, providerID, at.Unix(), records, "ok", snapshot, checksum, issues)
	return err
}
func (p *PostgresStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	if p.pool == nil {
		return ImportRow{}, false
	}
	var r ImportRow
	err := p.pool.QueryRow(ctx, `SELECT provider_id, at, records, status, snapshot, checksum, issues FROM imports WHERE provider_id=$1`, providerID).Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues)
	if err != nil {
		return ImportRow{}, false
	}
	return r, true
}
func (p *PostgresStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	if p.pool == nil {
		return StationRow{}, false
	}
	var r StationRow
	err := p.pool.QueryRow(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=$1 AND region_code=$2 LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil || (r.Lat == 0 && r.Lon == 0) {
		return StationRow{}, false
	}
	return r, true
}
func (p *PostgresStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	if p.pool == nil {
		return StationRow{}, false
	}
	var r StationRow
	err := p.pool.QueryRow(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=$1 AND region_code=$2 LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil {
		return StationRow{}, false
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

type pgTxStore struct {
	tx     pgx.Tx
	parent *PostgresStore
}

func (t *pgTxStore) Migrate(ctx context.Context) error                            { return nil }
func (t *pgTxStore) Close() error                                                 { return nil }
func (t *pgTxStore) WithTx(ctx context.Context, fn func(store.Store) error) error { return fn(t) }
func (t *pgTxStore) UpsertCity(ctx context.Context, c CityRow) (int64, error) {
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO cities(name, region_code, lat, lon, timezone, population, kind, source) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(name, region_code) DO UPDATE SET lat=EXCLUDED.lat, lon=EXCLUDED.lon RETURNING id`, c.Name, c.RegionCode, c.Lat, c.Lon, c.Timezone, c.Population, c.Kind, c.Source).Scan(&id)
	return id, err
}
func (t *pgTxStore) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	if s.ID != 0 {
		var id int64
		err := t.tx.QueryRow(ctx, `INSERT INTO stations(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name, lat=EXCLUDED.lat, lon=EXCLUDED.lon RETURNING id`, s.ID, s.Name, s.Lat, s.Lon, s.GeoCell, s.RegionCode, s.Timezone, s.QualityFlags, s.PrimaryProvider, s.CityID).Scan(&id)
		return s.ID, err
	}
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO stations(name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(name, region_code) DO UPDATE SET lat=EXCLUDED.lat, lon=EXCLUDED.lon RETURNING id`, s.Name, s.Lat, s.Lon, s.GeoCell, s.RegionCode, s.Timezone, s.QualityFlags, s.PrimaryProvider, s.CityID).Scan(&id)
	return id, err
}
func (t *pgTxStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	var id int64
	if s.ID != 0 {
		err := t.tx.QueryRow(ctx, `INSERT INTO stops(id, station_id, provider_id, external_code, stop_type, transport_type, name, raw_name) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(provider_id, external_code) DO UPDATE SET station_id=EXCLUDED.station_id RETURNING id`, s.ID, s.StationID, s.ProviderID, s.ExternalCode, s.StopType, s.TransportType, s.Name, s.RawName).Scan(&id)
		if err == nil {
			return s.ID, nil
		}
		return 0, err
	}
	err := t.tx.QueryRow(ctx, `INSERT INTO stops(station_id, provider_id, external_code, stop_type, transport_type, name, raw_name) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(provider_id, external_code) DO UPDATE SET station_id=EXCLUDED.station_id RETURNING id`, s.StationID, s.ProviderID, s.ExternalCode, s.StopType, s.TransportType, s.Name, s.RawName).Scan(&id)
	return id, err
}
func (t *pgTxStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO station_codes(station_id, provider_id, code_type, code, name_form, address) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(station_id, provider_id, code_type) DO UPDATE SET code=EXCLUDED.code`, c.StationID, c.ProviderID, c.CodeType, c.Code, c.NameForm, c.Address)
	return err
}
func (t *pgTxStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO carriers(provider_id, name, code, inn, address, iata, icao, sirena) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(provider_id, code) DO UPDATE SET name=EXCLUDED.name RETURNING id`, c.ProviderID, c.Name, c.Code, c.INN, c.Address, c.IATA, c.ICAO, c.Sirena).Scan(&id)
	if err != nil {
		_ = t.tx.QueryRow(ctx, `SELECT id FROM carriers WHERE provider_id=$1 AND code=$2`, c.ProviderID, c.Code).Scan(&id)
	}
	return id, err
}
func (t *pgTxStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	var id int64
	err := t.tx.QueryRow(ctx, `INSERT INTO routes(provider_id, carrier_id, external_code, short_name, long_name, mode, external_uid, ord, source_provider) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$1) ON CONFLICT(provider_id, external_code) DO UPDATE SET long_name=EXCLUDED.long_name RETURNING id`, r.ProviderID, r.CarrierID, r.ExternalCode, r.ShortName, r.LongName, r.Mode, r.ExternalUID, r.Ord).Scan(&id)
	return id, err
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
	_, err := t.tx.Exec(ctx, `INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, pickup_type, drop_off_type, dwell) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(trip_id, stop_id, seq) DO UPDATE SET arrival=EXCLUDED.arrival`, st.TripID, st.StopID, st.Seq, st.Arrival, st.Departure, st.PickupType, st.DropOffType, st.Dwell)
	return err
}
func (t *pgTxStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO transfers(from_stop_id, to_stop_id, minutes, min_transfer_time, distance_m, within_station) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(from_stop_id, to_stop_id) DO UPDATE SET minutes=EXCLUDED.minutes`, tr.FromStopID, tr.ToStopID, tr.Minutes, tr.MinTransferTime, tr.DistanceM, tr.WithinStation)
	return err
}
func (t *pgTxStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }
func (t *pgTxStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, code, msg, at) VALUES($1,$2,$3,$4,$5,$6,$7)`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Code, q.Msg, q.At)
	return err
}
func (t *pgTxStore) ClearQualityIssues(ctx context.Context, providerID string) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM quality_issues WHERE provider_id=$1`, providerID)
	return err
}
func (t *pgTxStore) ClearProviderData(ctx context.Context, providerID string) error {
	_, _ = t.tx.Exec(ctx, `DELETE FROM stop_times WHERE trip_id IN (SELECT id FROM trips WHERE provider_id=$1)`, providerID)
	_, _ = t.tx.Exec(ctx, `DELETE FROM stops WHERE provider_id=$1`, providerID)
	_, _ = t.tx.Exec(ctx, `DELETE FROM trips WHERE provider_id=$1`, providerID)
	_, _ = t.tx.Exec(ctx, `DELETE FROM routes WHERE provider_id=$1`, providerID)
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
	var id int64
	if r.ID != 0 {
		err := t.tx.QueryRow(ctx, `INSERT INTO terminals(id, place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES($1,$2,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography,$5, daterange($6::date, $7::date, '[]'), $8, $9, $10, $11) ON CONFLICT(id) DO UPDATE SET place_id=EXCLUDED.place_id, geom=EXCLUDED.geom, tz=EXCLUDED.tz RETURNING id`, r.ID, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt).Scan(&id)
		if err != nil {
			return 0, err
		}
	} else {
		err := t.tx.QueryRow(ctx, `INSERT INTO terminals(place_id, geom, tz, validity, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES($1,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,$4, daterange($5::date, $6::date, '[]'), $7, $8, $9, $10) RETURNING id`, r.PlaceID, r.Lon, r.Lat, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt).Scan(&id)
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
	_, err := t.tx.Exec(ctx, `INSERT INTO review_queue(entity_type, entity_id, reason, score) VALUES($1,$2,$3,$4) ON CONFLICT(entity_type, entity_id) DO UPDATE SET reason=EXCLUDED.reason, score=EXCLUDED.score`, e.EntityType, e.EntityID, e.Reason, e.Score)
	return err
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
	_, err := t.tx.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status) VALUES($1,$2,$3,$4) ON CONFLICT(provider_id) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records`, providerID, at.Unix(), records, "ok")
	return err
}
func (t *pgTxStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO imports(provider_id, at, records, status, snapshot, checksum, issues) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(provider_id) DO UPDATE SET at=EXCLUDED.at, records=EXCLUDED.records, snapshot=EXCLUDED.snapshot, checksum=EXCLUDED.checksum, issues=EXCLUDED.issues`, providerID, at.Unix(), records, "ok", snapshot, checksum, issues)
	return err
}
func (t *pgTxStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	var r ImportRow
	err := t.tx.QueryRow(ctx, `SELECT provider_id, at, records, status, snapshot, checksum, issues FROM imports WHERE provider_id=$1`, providerID).Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues)
	if err != nil {
		return ImportRow{}, false
	}
	return r, true
}
func (t *pgTxStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	var r StationRow
	err := t.tx.QueryRow(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=$1 AND region_code=$2 LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil || (r.Lat == 0 && r.Lon == 0) {
		return StationRow{}, false
	}
	return r, true
}
func (t *pgTxStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	var r StationRow
	err := t.tx.QueryRow(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=$1 AND region_code=$2 LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil {
		return StationRow{}, false
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

func init() {
	store.Register("postgres", func(ctx context.Context, dsn string) (store.Store, error) {
		return NewPostgresStore(ctx, dsn)
	})
}

var errNotImplemented = errStr("postgres store: метод ещё не реализован")

type errStr string

func (e errStr) Error() string { return string(e) }

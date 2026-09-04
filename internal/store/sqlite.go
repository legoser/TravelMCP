package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"travelmcp/internal/model"
	"travelmcp/internal/support/classifier"
)

// Deprecated: SQLiteStore сохранён только для тестов и неразрушающей совместимости.
// Для продакшена используйте PostgresStore (PostgreSQL+PostGIS, migrations/001...sql).
// Новые фичи фазы 2+ не будут портироваться на SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// Deprecated: используйте store.New с postgres DSN.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	if dsn == "memory" {
		dsn = "file::memory:?cache=shared"
	} else if strings.HasPrefix(dsn, "sqlite://") {
		dsn = strings.TrimPrefix(dsn, "sqlite://")
		if dsn == "" || dsn == ":memory:" {
			dsn = "file::memory:?cache=shared"
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, fmt.Errorf("pragma wal: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		return nil, fmt.Errorf("pragma sync: %w", err)
	}
	if _, err := db.Exec(`PRAGMA cache_size=-64000`); err != nil {
		return nil, fmt.Errorf("pragma cache: %w", err)
	}
	if _, err := db.Exec(`PRAGMA temp_store=MEMORY`); err != nil {
		return nil, fmt.Errorf("pragma temp: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return nil, fmt.Errorf("pragma fk: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Deprecated: прямой доступ к *sql.DB нарушает инкапсуляцию репозитория.
// Оставлен для тестов places.Service, будет удалён после перехода на Store-интерфейс.
func (s *SQLiteStore) DB() *sql.DB  { return s.db }
func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) WithTx(ctx context.Context, fn func(Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	ts := &txStore{tx: tx, parent: s}
	if err := fn(ts); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	var r StationRow
	err := s.db.QueryRowContext(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=? AND region_code=? LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil {
		return StationRow{}, false
	}
	if r.Lat == 0 && r.Lon == 0 {
		return StationRow{}, false
	}
	return r, true
}

func (s *SQLiteStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	var r StationRow
	err := s.db.QueryRowContext(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=? AND region_code=? LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil {
		return StationRow{}, false
	}
	return r, true
}

type txStore struct {
	tx     *sql.Tx
	parent *SQLiteStore
}

func (t *txStore) Close() error                      { return nil }
func (t *txStore) Migrate(ctx context.Context) error { return nil }
func (t *txStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	return t.parent.LoadNetwork(ctx, providers, day)
}
func (t *txStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO imports(provider_id, at, records, status) VALUES(?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET at=excluded.at, records=excluded.records`, providerID, at.Unix(), records, "ok")
	return err
}
func (t *txStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO imports(provider_id, at, records, status, snapshot, checksum, issues) VALUES(?,?,?,?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET at=excluded.at, records=excluded.records, snapshot=excluded.snapshot, checksum=excluded.checksum, issues=excluded.issues`, providerID, at.Unix(), records, "ok", snapshot, checksum, issues)
	return err
}
func (t *txStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	var r ImportRow
	err := t.tx.QueryRowContext(ctx, `SELECT provider_id, at, records, status, snapshot, checksum, issues FROM imports WHERE provider_id=?`, providerID).Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues)
	if err != nil {
		// fallback to parent
		return t.parent.GetImport(ctx, providerID)
	}
	return r, true
}
func (t *txStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, code, msg, at) VALUES(?,?,?,?,?,?,?)`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Code, q.Msg, q.At)
	return err
}
func (t *txStore) ClearQualityIssues(ctx context.Context, providerID string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM quality_issues WHERE provider_id=?`, providerID)
	return err
}
func (t *txStore) ClearProviderData(ctx context.Context, providerID string) error {
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM stop_times WHERE trip_id IN (SELECT id FROM trips WHERE provider_id=?)`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM transfers WHERE from_stop_id IN (SELECT id FROM stops WHERE provider_id=?) OR to_stop_id IN (SELECT id FROM stops WHERE provider_id=?)`, providerID, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM station_codes WHERE provider_id=?`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM stops WHERE provider_id=?`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM trips WHERE provider_id=?`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM routes WHERE provider_id=?`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM carriers WHERE provider_id=?`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM service_days WHERE service_id IN (SELECT id FROM services WHERE provider_id=?)`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM service_exceptions WHERE service_id IN (SELECT id FROM services WHERE provider_id=?)`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM services WHERE provider_id=?`, providerID)
	_, _ = t.tx.ExecContext(ctx, `DELETE FROM stations WHERE primary_provider=?`, providerID)
	return nil
}
func (t *txStore) UpsertService(ctx context.Context, r ServiceRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO services(id, provider_id, name, start_date, end_date) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, start_date=excluded.start_date, end_date=excluded.end_date`, r.ID, r.ProviderID, r.Name, r.StartDate, r.EndDate)
	return err
}
func (t *txStore) UpsertServiceDay(ctx context.Context, r ServiceDayRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO service_days(service_id, weekday) VALUES(?,?) ON CONFLICT(service_id, weekday) DO NOTHING`, r.ServiceID, r.Weekday)
	return err
}
func (t *txStore) UpsertServiceException(ctx context.Context, r ServiceExceptionRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO service_exceptions(service_id, date, exception_type) VALUES(?,?,?) ON CONFLICT(service_id, date) DO UPDATE SET exception_type=excluded.exception_type`, r.ServiceID, r.Date, r.ExceptionType)
	return err
}
func (t *txStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }
func (t *txStore) UpsertCity(ctx context.Context, c CityRow) (int64, error) {
	res, err := t.tx.ExecContext(ctx, `INSERT INTO cities(name, region_code, lat, lon, timezone, population, kind, source) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(name, region_code) DO UPDATE SET lat=excluded.lat, lon=excluded.lon`, c.Name, c.RegionCode, c.Lat, c.Lon, c.Timezone, c.Population, c.Kind, c.Source)
	if err != nil {
		var existing int64
		if e2 := t.tx.QueryRowContext(ctx, `SELECT id FROM cities WHERE name=? AND region_code=?`, c.Name, c.RegionCode).Scan(&existing); e2 == nil {
			_, _ = t.tx.ExecContext(ctx, `UPDATE cities SET lat=?, lon=?, timezone=?, kind=? WHERE id=?`, c.Lat, c.Lon, c.Timezone, c.Kind, existing)
			return existing, nil
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = t.tx.QueryRowContext(ctx, `SELECT id FROM cities WHERE name=? AND region_code=?`, c.Name, c.RegionCode).Scan(&id)
	}
	return id, nil
}

func (t *txStore) UpsertStation(ctx context.Context, r StationRow) (int64, error) {
	if r.ID != 0 {
		_, err := t.tx.ExecContext(ctx, `INSERT INTO stations(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, lat=excluded.lat, lon=excluded.lon, geo_cell=excluded.geo_cell, quality_flags=excluded.quality_flags, city_id=excluded.city_id`, r.ID, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider, r.CityID)
		return r.ID, err
	}
	res, err := t.tx.ExecContext(ctx, `INSERT INTO stations(name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(name, region_code) DO UPDATE SET lat=excluded.lat, lon=excluded.lon, geo_cell=excluded.geo_cell, quality_flags=excluded.quality_flags, city_id=excluded.city_id`, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider, r.CityID)
	if err != nil {
		var existing int64
		if e2 := t.tx.QueryRowContext(ctx, `SELECT id FROM stations WHERE name=? AND region_code=?`, r.Name, r.RegionCode).Scan(&existing); e2 == nil {
			_, _ = t.tx.ExecContext(ctx, `UPDATE stations SET lat=?, lon=?, geo_cell=?, quality_flags=?, primary_provider=?, city_id=? WHERE id=?`, r.Lat, r.Lon, r.GeoCell, r.QualityFlags, r.PrimaryProvider, r.CityID, existing)
			return existing, nil
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		var existing int64
		_ = t.tx.QueryRowContext(ctx, `SELECT id FROM stations WHERE name=? AND region_code=?`, r.Name, r.RegionCode).Scan(&existing)
		if existing != 0 {
			return existing, nil
		}
	}
	return id, nil
}
func (t *txStore) UpsertStop(ctx context.Context, r StopRow) (int64, error) {
	res, err := t.tx.ExecContext(ctx, `INSERT INTO stops(station_id, provider_id, external_code, stop_type, transport_type, name, raw_name) VALUES(?,?,?,?,?,?,?) ON CONFLICT(provider_id, external_code) DO UPDATE SET station_id=excluded.station_id, name=excluded.name`, r.StationID, r.ProviderID, r.ExternalCode, r.StopType, r.TransportType, r.Name, r.RawName)
	if err != nil {
		return 0, err
	}
	if r.ID != 0 {
		return r.ID, nil
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = t.tx.QueryRowContext(ctx, `SELECT id FROM stops WHERE provider_id=? AND external_code=?`, r.ProviderID, r.ExternalCode).Scan(&id)
	}
	return id, nil
}
func (t *txStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO station_codes(station_id, provider_id, code_type, code, name_form, address) VALUES(?,?,?,?,?,?) ON CONFLICT(station_id, provider_id, code_type) DO UPDATE SET code=excluded.code`, c.StationID, c.ProviderID, c.CodeType, c.Code, c.NameForm, c.Address)
	return err
}
func (t *txStore) UpsertCarrier(ctx context.Context, r CarrierRow) (int64, error) {
	res, err := t.tx.ExecContext(ctx, `INSERT INTO carriers(provider_id, name, code, inn, address, iata, icao, sirena) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider_id, code) DO UPDATE SET name=excluded.name, inn=excluded.inn, address=excluded.address`, r.ProviderID, r.Name, r.Code, r.INN, r.Address, r.IATA, r.ICAO, r.Sirena)
	if err != nil {
		return 0, err
	}
	if r.ID != 0 {
		return r.ID, nil
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = t.tx.QueryRowContext(ctx, `SELECT id FROM carriers WHERE provider_id=? AND code=?`, r.ProviderID, r.Code).Scan(&id)
	}
	return id, nil
}
func (t *txStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	res, err := t.tx.ExecContext(ctx, `INSERT INTO routes(provider_id, carrier_id, external_code, short_name, long_name, mode, external_uid, ord) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider_id, external_code) DO UPDATE SET long_name=excluded.long_name`, r.ProviderID, r.CarrierID, r.ExternalCode, r.ShortName, r.LongName, r.Mode, r.ExternalUID, r.Ord)
	if err != nil {
		return 0, err
	}
	if r.ID != 0 {
		return r.ID, nil
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = t.tx.QueryRowContext(ctx, `SELECT id FROM routes WHERE provider_id=? AND external_code=?`, r.ProviderID, r.ExternalCode).Scan(&id)
	}
	return id, nil
}
func (t *txStore) UpsertTrip(ctx context.Context, r TripRow) (int64, error) {
	res, err := t.tx.ExecContext(ctx, `INSERT INTO trips(route_id, provider_id, direction, service_days, frequency_flag, period, service_id) VALUES(?,?,?,?,?,?,?)`, r.RouteID, r.ProviderID, r.Direction, r.ServiceDays, r.FrequencyFlag, r.Period, r.ServiceID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (t *txStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error { return nil }
func (t *txStore) UpsertStopTime(ctx context.Context, r StopTimeRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, pickup_type, drop_off_type, dwell) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(trip_id, stop_id, seq) DO UPDATE SET arrival=excluded.arrival`, r.TripID, r.StopID, r.Seq, r.Arrival, r.Departure, r.PickupType, r.DropOffType, r.Dwell)
	return err
}
func (t *txStore) UpsertTransfer(ctx context.Context, r TransferRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO transfers(from_stop_id, to_stop_id, minutes, min_transfer_time, distance_m, within_station) VALUES(?,?,?,?,?,?) ON CONFLICT(from_stop_id, to_stop_id) DO UPDATE SET minutes=excluded.minutes`, r.FromStopID, r.ToStopID, r.Minutes, r.MinTransferTime, r.DistanceM, r.WithinStation)
	return err
}
func (t *txStore) WithTx(ctx context.Context, fn func(Store) error) error {
	return fn(t)
}
func (t *txStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	var r StationRow
	err := t.tx.QueryRowContext(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=? AND region_code=? LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil || (r.Lat == 0 && r.Lon == 0) {
		return StationRow{}, false
	}
	return r, true
}

func (t *txStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	var r StationRow
	err := t.tx.QueryRowContext(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations WHERE name=? AND region_code=? LIMIT 1`, name, region).Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
	if err != nil {
		return StationRow{}, false
	}
	return r, true
}
func (t *txStore) UpsertPlace(ctx context.Context, r PlaceRow, names map[string]string) (int64, error) {
	var id int64
	if r.ID != 0 {
		_, err := t.tx.ExecContext(ctx, `INSERT INTO places(id, parent_id, admin_level, level, lat, lon, tz, valid_from, valid_to) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, admin_level=excluded.admin_level, level=excluded.level, lat=excluded.lat, lon=excluded.lon, tz=excluded.tz, valid_to=excluded.valid_to`, r.ID, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo)
		if err != nil {
			return 0, err
		}
		id = r.ID
	} else {
		res, err := t.tx.ExecContext(ctx, `INSERT INTO places(parent_id, admin_level, level, lat, lon, tz, valid_from, valid_to) VALUES(?,?,?,?,?,?,?,?)`, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	}
	for lang, name := range names {
		norm := strings.ToLower(strings.TrimSpace(name))
		norm = strings.Join(strings.Fields(norm), " ")
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO place_names(place_id, lang, name, normalized) VALUES(?,?,?,?) ON CONFLICT(place_id, lang) DO UPDATE SET name=excluded.name, normalized=excluded.normalized`, id, lang, name, norm); err != nil {
			return 0, err
		}
	}
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM place_closure WHERE descendant_id=?`, id); err != nil {
		return 0, err
	}
	if _, err := t.tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,0)`, id, id); err != nil {
		return 0, err
	}
	var parent sql.NullInt64
	_ = t.tx.QueryRowContext(ctx, `SELECT parent_id FROM places WHERE id=?`, id).Scan(&parent)
	if parent.Valid {
		rows, err := t.tx.QueryContext(ctx, `SELECT ancestor_id, depth FROM place_closure WHERE descendant_id=?`, parent.Int64)
		if err == nil {
			type ancRow struct {
				anc int64
				d   int
			}
			var ancestors []ancRow
			for rows.Next() {
				var anc int64
				var d int
				rows.Scan(&anc, &d)
				ancestors = append(ancestors, ancRow{anc, d})
			}
			rows.Close()
			for _, a := range ancestors {
				t.tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,?)`, a.anc, id, a.d+1)
			}
		}
	}
	return id, nil
}

func (t *txStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	var id int64
	if r.ID != 0 {
		_, err := t.tx.ExecContext(ctx, `INSERT INTO terminals(id, place_id, lat, lon, tz, validity_from, validity_to, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET place_id=excluded.place_id, lat=excluded.lat, lon=excluded.lon`, r.ID, r.PlaceID, r.Lat, r.Lon, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt)
		if err != nil {
			return 0, err
		}
		id = r.ID
	} else {
		res, err := t.tx.ExecContext(ctx, `INSERT INTO terminals(place_id, lat, lon, tz, validity_from, validity_to, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.PlaceID, r.Lat, r.Lon, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	}
	for lang, name := range names {
		t.tx.ExecContext(ctx, `INSERT INTO terminal_names(terminal_id, lang, name, is_primary) VALUES(?,?,?,?) ON CONFLICT(terminal_id, lang) DO UPDATE SET name=excluded.name`, id, lang, name, 1)
	}
	if ru, ok := names["ru"]; ok {
		lower := strings.ToLower(ru)
		t.tx.ExecContext(ctx, `UPDATE terminals SET osm_compatible_name=? WHERE id=?`, lower, id)
	}
	for _, ident := range identifiers {
		t.tx.ExecContext(ctx, `INSERT INTO terminal_identifiers(terminal_id, system, code_type, code, is_primary) VALUES(?,?,?,?,?) ON CONFLICT(terminal_id, system, code_type) DO UPDATE SET code=excluded.code`, id, ident.System, ident.CodeType, ident.Code, 0)
	}
	return id, nil
}
func (t *txStore) SaveProvenance(ctx context.Context, p model.Provenance) error {
	raw := ""
	if len(p.Raw) > 0 {
		raw = string(p.Raw)
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, raw, actor_id) VALUES(?,?,?,?,?,?,?) ON CONFLICT(entity_type, entity_id, source) DO UPDATE SET confidence=excluded.confidence`, p.EntityType, p.EntityID, p.Source, p.Confidence, p.ObservedAt.Unix(), raw, p.ActorID)
	return err
}
func (t *txStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO review_queue(entity_type, entity_id, reason, score) VALUES(?,?,?,?) ON CONFLICT(entity_type, entity_id) DO UPDATE SET reason=excluded.reason`, e.EntityType, e.EntityID, e.Reason, e.Score)
	return err
}
func (t *txStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	var id int64
	var tz sql.NullString
	err := t.tx.QueryRowContext(ctx, `SELECT p.id, p.tz FROM place_closure pc JOIN places p ON p.id=pc.ancestor_id WHERE pc.descendant_id=? AND p.level=4 LIMIT 1`, placeID).Scan(&id, &tz)
	if err != nil {
		return 0, "", err
	}
	return id, tz.String, nil
}
func (t *txStore) ImportAdaptedRecords(ctx context.Context, records []model.AdaptedRecord) (int, int, error) {
	return 0, 0, fmt.Errorf("ImportAdaptedRecords: use SQLiteStore directly")
}

func (t *txStore) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	status := "pending"
	if role == "admin" {
		status = "active"
	}
	res, err := t.tx.ExecContext(ctx, `INSERT INTO users(email, pass_hash, status, role, created_at, config) VALUES(?,?,?,?,?,?)`, email, passHash, status, role, time.Now().Unix(), "")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (t *txStore) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	var r UserRow
	err := t.tx.QueryRowContext(ctx, `SELECT id, email, pass_hash, status, role, created_at, config FROM users WHERE email=?`, email).Scan(&r.ID, &r.Email, &r.PassHash, &r.Status, &r.Role, &r.CreatedAt, &r.Config)
	if err != nil {
		return UserRow{}, false
	}
	return r, true
}
func (t *txStore) GetUserByID(ctx context.Context, id int64) (UserRow, bool) {
	var r UserRow
	err := t.tx.QueryRowContext(ctx, `SELECT id, email, pass_hash, status, role, created_at, config FROM users WHERE id=?`, id).Scan(&r.ID, &r.Email, &r.PassHash, &r.Status, &r.Role, &r.CreatedAt, &r.Config)
	if err != nil {
		return UserRow{}, false
	}
	return r, true
}
func (t *txStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT id, email, pass_hash, status, role, created_at, config FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		rows.Scan(&r.ID, &r.Email, &r.PassHash, &r.Status, &r.Role, &r.CreatedAt, &r.Config)
		out = append(out, r)
	}
	return out, nil
}
func (t *txStore) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE users SET status=? WHERE id=?`, status, id)
	return err
}
func (t *txStore) UpdateUserRole(ctx context.Context, id int64, role string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
	return err
}
func (t *txStore) UpdateUserConfig(ctx context.Context, id int64, config string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE users SET config=? WHERE id=?`, config, id)
	return err
}
func (t *txStore) DeleteUser(ctx context.Context, id int64) error {
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id=?`, id); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}
func (t *txStore) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	key := generateApiKey()
	if scopes == "" {
		scopes = "mcp:read"
	}
	res, err := t.tx.ExecContext(ctx, `INSERT INTO api_keys(user_id, key, scopes, created_at, last_used) VALUES(?,?,?,?,?)`, userID, key, scopes, time.Now().Unix(), 0)
	if err != nil {
		return ApiKeyRow{}, err
	}
	id, _ := res.LastInsertId()
	return ApiKeyRow{ID: id, UserID: userID, Key: key, Scopes: scopes, CreatedAt: time.Now().Unix()}, nil
}
func (t *txStore) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) {
	var r ApiKeyRow
	err := t.tx.QueryRowContext(ctx, `SELECT id, user_id, key, scopes, created_at, last_used FROM api_keys WHERE key=?`, key).Scan(&r.ID, &r.UserID, &r.Key, &r.Scopes, &r.CreatedAt, &r.LastUsed)
	if err != nil {
		return ApiKeyRow{}, false
	}
	return r, true
}
func (t *txStore) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT id, user_id, key, scopes, created_at, last_used FROM api_keys WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApiKeyRow
	for rows.Next() {
		var r ApiKeyRow
		rows.Scan(&r.ID, &r.UserID, &r.Key, &r.Scopes, &r.CreatedAt, &r.LastUsed)
		out = append(out, r)
	}
	return out, nil
}
func (t *txStore) DeleteApiKey(ctx context.Context, id int64, userID int64) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM api_keys WHERE id=? AND user_id=?`, id, userID)
	return err
}
func (t *txStore) TouchApiKey(ctx context.Context, key string) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE api_keys SET last_used=? WHERE key=?`, time.Now().Unix(), key)
	return err
}

func (s *SQLiteStore) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS regions (
			code TEXT PRIMARY KEY,
			name_ru TEXT NOT NULL,
			name_en TEXT,
			geom TEXT,
			density_class TEXT CHECK(density_class IN ('rural','suburban','urban','metro')) DEFAULT 'rural'
		)`,
		`CREATE TABLE IF NOT EXISTS providers (
			code TEXT PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS carriers_pg (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			inn TEXT, name_ru TEXT NOT NULL, name_en TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS carrier_identifiers (
			carrier_id INTEGER NOT NULL REFERENCES carriers(id) ON DELETE CASCADE,
			system TEXT NOT NULL, code_type TEXT NOT NULL, code TEXT NOT NULL,
			PRIMARY KEY(carrier_id, system, code_type),
			UNIQUE(system, code)
		)`,
		`CREATE TABLE IF NOT EXISTS places (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			parent_id INTEGER REFERENCES places(id) DEFERRABLE INITIALLY DEFERRED,
			admin_level INTEGER NOT NULL, level INTEGER NOT NULL,
			lat REAL, lon REAL, tz TEXT,
			bbox_min_lat REAL, bbox_min_lon REAL, bbox_max_lat REAL, bbox_max_lon REAL,
			valid_from TEXT NOT NULL DEFAULT (date('now')), valid_to TEXT, is_current INTEGER GENERATED ALWAYS AS (CASE WHEN valid_to IS NULL THEN 1 ELSE 0 END) STORED
		)`,
		`CREATE TABLE IF NOT EXISTS place_closure (
			ancestor_id INTEGER NOT NULL REFERENCES places(id) ON DELETE CASCADE,
			descendant_id INTEGER NOT NULL REFERENCES places(id) ON DELETE CASCADE,
			depth INTEGER NOT NULL,
			PRIMARY KEY(ancestor_id, descendant_id)
		)`,
		`CREATE TABLE IF NOT EXISTS place_names (
			place_id INTEGER NOT NULL REFERENCES places(id) ON DELETE CASCADE,
			lang TEXT NOT NULL CHECK(lang IN ('ru','en')),
			name TEXT NOT NULL, normalized TEXT NOT NULL,
			PRIMARY KEY(place_id, lang)
		)`,
		`CREATE TABLE IF NOT EXISTS terminals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			place_id INTEGER REFERENCES places(id) ON DELETE SET NULL,
			lat REAL NOT NULL, lon REAL NOT NULL, tz TEXT,
			validity_from TEXT, validity_to TEXT,
			osm_compatible_name TEXT,
			valid_from TEXT NOT NULL DEFAULT (date('now')), valid_to TEXT, is_current INTEGER GENERATED ALWAYS AS (CASE WHEN valid_to IS NULL THEN 1 ELSE 0 END) STORED,
			last_verified_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS stops_canonical (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			terminal_id INTEGER NOT NULL REFERENCES terminals(id) ON DELETE CASCADE,
			lat REAL, lon REAL, stop_type TEXT, validity_from TEXT, validity_to TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS terminal_names (
			terminal_id INTEGER NOT NULL REFERENCES terminals(id) ON DELETE CASCADE,
			lang TEXT NOT NULL CHECK(lang IN ('ru','en')),
			name TEXT NOT NULL, is_primary INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY(terminal_id, lang)
		)`,
		`CREATE TABLE IF NOT EXISTS stop_names (
			stop_id INTEGER NOT NULL REFERENCES stops_canonical(id) ON DELETE CASCADE,
			lang TEXT NOT NULL CHECK(lang IN ('ru','en')),
			name TEXT NOT NULL,
			PRIMARY KEY(stop_id, lang)
		)`,
		`CREATE TABLE IF NOT EXISTS terminal_identifiers (
			terminal_id INTEGER NOT NULL REFERENCES terminals(id) ON DELETE CASCADE,
			system TEXT NOT NULL, code_type TEXT NOT NULL, code TEXT NOT NULL, is_primary INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(terminal_id, system, code_type),
			UNIQUE(system, code)
		)`,
		`CREATE TABLE IF NOT EXISTS terminal_tags (
			terminal_id INTEGER NOT NULL REFERENCES terminals(id) ON DELETE CASCADE PRIMARY KEY,
			tags TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS provenance (
			entity_type TEXT NOT NULL CHECK(entity_type IN ('place','terminal','stop','route','trip')),
			entity_id INTEGER NOT NULL, source TEXT NOT NULL, confidence REAL NOT NULL CHECK(confidence>=0 AND confidence<=1),
			observed_at INTEGER NOT NULL, raw TEXT, actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			PRIMARY KEY(entity_type, entity_id, source)
		)`,
		`CREATE TABLE IF NOT EXISTS review_queue (
			entity_type TEXT NOT NULL CHECK(entity_type IN ('place','terminal','stop','route','trip')),
			entity_id INTEGER NOT NULL, reason TEXT NOT NULL, score REAL, created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
			PRIMARY KEY(entity_type, entity_id)
		)`,
		`CREATE TABLE IF NOT EXISTS cities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL, region_code TEXT NOT NULL DEFAULT '', lat REAL NOT NULL, lon REAL NOT NULL, timezone TEXT, population INTEGER, kind TEXT, source TEXT,
			UNIQUE(name, region_code)
		)`,
		`CREATE TABLE IF NOT EXISTS stations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL, lat REAL NOT NULL DEFAULT 0, lon REAL NOT NULL DEFAULT 0, geo_cell INTEGER,
			region_code TEXT NOT NULL DEFAULT '', timezone TEXT, quality_flags INTEGER, primary_provider TEXT, city_id INTEGER REFERENCES cities(id) ON DELETE SET NULL,
			UNIQUE(name, region_code),
			FOREIGN KEY(city_id) REFERENCES cities(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS stops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id INTEGER NOT NULL REFERENCES stations(id) ON DELETE CASCADE ON UPDATE CASCADE,
			provider_id TEXT NOT NULL, external_code TEXT NOT NULL, stop_type TEXT, transport_type TEXT, name TEXT, raw_name TEXT,
			UNIQUE(provider_id, external_code),
			FOREIGN KEY(station_id) REFERENCES stations(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS station_codes (
			station_id INTEGER NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
			provider_id TEXT, code_type TEXT, code TEXT, name_form TEXT, address TEXT,
			PRIMARY KEY(station_id, provider_id, code_type),
			FOREIGN KEY(station_id) REFERENCES stations(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS carriers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id TEXT NOT NULL, name TEXT, code TEXT NOT NULL, inn TEXT, address TEXT, iata TEXT, icao TEXT, sirena TEXT,
			UNIQUE(provider_id, code)
		)`,
		`CREATE TABLE IF NOT EXISTS routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id TEXT NOT NULL, carrier_id INTEGER REFERENCES carriers(id) ON DELETE SET NULL,
			external_code TEXT NOT NULL, short_name TEXT, long_name TEXT, mode TEXT, external_uid TEXT, ord INTEGER,
			UNIQUE(provider_id, external_code),
			FOREIGN KEY(carrier_id) REFERENCES carriers(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS trips (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
			provider_id TEXT NOT NULL, direction TEXT, service_days TEXT, frequency_flag INTEGER, period TEXT, service_id INTEGER REFERENCES services(id) ON DELETE SET NULL,
			FOREIGN KEY(route_id) REFERENCES routes(id) ON DELETE CASCADE,
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS stop_times (
			trip_id INTEGER NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
			stop_id INTEGER NOT NULL REFERENCES stops(id) ON DELETE CASCADE,
			seq INTEGER, arrival INTEGER, departure INTEGER, pickup_type INTEGER, drop_off_type INTEGER, dwell INTEGER,
			PRIMARY KEY(trip_id, stop_id, seq),
			FOREIGN KEY(trip_id) REFERENCES trips(id) ON DELETE CASCADE,
			FOREIGN KEY(stop_id) REFERENCES stops(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS transfers (
			from_stop_id INTEGER NOT NULL REFERENCES stops(id) ON DELETE CASCADE,
			to_stop_id INTEGER NOT NULL REFERENCES stops(id) ON DELETE CASCADE,
			minutes INTEGER, min_transfer_time INTEGER, distance_m INTEGER, within_station INTEGER,
			PRIMARY KEY(from_stop_id, to_stop_id),
			FOREIGN KEY(from_stop_id) REFERENCES stops(id) ON DELETE CASCADE,
			FOREIGN KEY(to_stop_id) REFERENCES stops(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS quality_issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id TEXT, entity TEXT, entity_id TEXT, level TEXT, code TEXT, msg TEXT, at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS services (
			id INTEGER PRIMARY KEY,
			provider_id TEXT NOT NULL, name TEXT, start_date TEXT, end_date TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS service_days (
			service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
			weekday INTEGER,
			PRIMARY KEY(service_id, weekday),
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS service_exceptions (
			service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
			date TEXT, exception_type TEXT,
			PRIMARY KEY(service_id, date),
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS imports (
			provider_id TEXT PRIMARY KEY, at INTEGER, records INTEGER, status TEXT, snapshot TEXT, checksum TEXT, issues INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE, pass_hash TEXT, status TEXT, role TEXT, created_at INTEGER, config TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, key TEXT UNIQUE, scopes TEXT, created_at INTEGER, last_used INTEGER,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stops_station ON stops(station_id)`,
		`CREATE INDEX IF NOT EXISTS idx_routes_external ON routes(external_code)`,
		`CREATE INDEX IF NOT EXISTS idx_trips_route ON trips(route_id)`,
		`CREATE INDEX IF NOT EXISTS idx_stop_times_trip ON stop_times(trip_id, seq)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	for _, q := range []string{
		`ALTER TABLE quality_issues ADD COLUMN code TEXT`,
		`ALTER TABLE trips ADD COLUMN service_id INTEGER`,
		`ALTER TABLE stations ADD COLUMN city_id INTEGER REFERENCES cities(id) ON DELETE SET NULL`,
	} {
		_, _ = s.db.ExecContext(ctx, q)
	}
	for _, q := range []string{
		`CREATE INDEX IF NOT EXISTS idx_stop_times_stop_departure ON stop_times(stop_id, departure)`,
		`CREATE INDEX IF NOT EXISTS idx_transfers_from ON transfers(from_stop_id)`,
		`CREATE INDEX IF NOT EXISTS idx_transfers_to ON transfers(to_stop_id)`,
		`CREATE INDEX IF NOT EXISTS idx_station_geo ON stations(geo_cell)`,
		`CREATE INDEX IF NOT EXISTS idx_quality_provider_code ON quality_issues(provider_id, code)`,
		`CREATE INDEX IF NOT EXISTS idx_stations_name_region ON stations(name, region_code)`,
		`CREATE INDEX IF NOT EXISTS idx_trips_service ON trips(service_id)`,
		`CREATE INDEX IF NOT EXISTS idx_services_provider ON services(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cities_name_region ON cities(name, region_code)`,
		`CREATE INDEX IF NOT EXISTS idx_stations_city ON stations(city_id)`,
		`CREATE INDEX IF NOT EXISTS idx_places_parent ON places(parent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_places_level ON places(level)`,
		`CREATE INDEX IF NOT EXISTS idx_place_closure_descendant ON place_closure(descendant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_place_closure_desc_depth ON place_closure(descendant_id, depth)`,
		`CREATE INDEX IF NOT EXISTS idx_place_names_norm ON place_names(normalized)`,
		`CREATE INDEX IF NOT EXISTS idx_terminals_place ON terminals(place_id)`,
		`CREATE INDEX IF NOT EXISTS idx_terminals_verified ON terminals(last_verified_at)`,
		`CREATE INDEX IF NOT EXISTS idx_terminal_names_lang ON terminal_names(terminal_id, lang)`,
		`CREATE INDEX IF NOT EXISTS idx_terminal_identifiers_code ON terminal_identifiers(system, code)`,
	} {
		_, _ = s.db.ExecContext(ctx, q)
	}
	for _, q := range []string{
		`INSERT OR IGNORE INTO providers(code, name) VALUES ('motis','MOTIS/OSM'), ('mintrans','Минтранс'), ('yandex','Яндекс'), ('osm','OSM'), ('gtfs','GTFS'), ('nominatim','Nominatim')`,
		`INSERT OR IGNORE INTO regions(code, name_ru, density_class) VALUES ('22','Алтайский край','rural'), ('42','Кемеровская область','rural'), ('54','Новосибирская область','suburban'), ('70','Томская область','rural')`,
	} {
		_, _ = s.db.ExecContext(ctx, q)
	}
	_, _ = s.db.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS trg_sync_osm_name_insert AFTER INSERT ON terminal_names FOR EACH ROW WHEN NEW.lang='ru' BEGIN UPDATE terminals SET osm_compatible_name = lower(NEW.name) WHERE id = NEW.terminal_id; END;`)
	_, _ = s.db.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS trg_sync_osm_name_update AFTER UPDATE OF name ON terminal_names FOR EACH ROW WHEN NEW.lang='ru' BEGIN UPDATE terminals SET osm_compatible_name = lower(NEW.name) WHERE id = NEW.terminal_id; END;`)
	_, _ = s.db.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS trg_sync_osm_name_delete AFTER DELETE ON terminal_names FOR EACH ROW WHEN OLD.lang='ru' BEGIN UPDATE terminals SET osm_compatible_name = (SELECT lower(name) FROM terminal_names WHERE terminal_id = OLD.terminal_id AND lang='ru' LIMIT 1) WHERE id = OLD.terminal_id; END;`)
	if err := s.deduplicateStations(ctx); err != nil {
		return fmt.Errorf("dedup stations: %w", err)
	}
	if err := s.migrateForeignKeys(ctx); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) migrateForeignKeys(ctx context.Context) error {
	var sql string
	err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='trips'`).Scan(&sql)
	if err == nil && sql != "" && !strings.Contains(sql, "REFERENCES services") {
		if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			return err
		}
		_, _ = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS trips_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
			provider_id TEXT NOT NULL, direction TEXT, service_days TEXT, frequency_flag INTEGER, period TEXT, service_id INTEGER REFERENCES services(id) ON DELETE SET NULL,
			FOREIGN KEY(route_id) REFERENCES routes(id) ON DELETE CASCADE,
			FOREIGN KEY(service_id) REFERENCES services(id) ON DELETE SET NULL
		)`)
		_, _ = s.db.ExecContext(ctx, `INSERT INTO trips_new(id, route_id, provider_id, direction, service_days, frequency_flag, period, service_id) SELECT id, route_id, provider_id, direction, service_days, frequency_flag, period, service_id FROM trips`)
		_, _ = s.db.ExecContext(ctx, `DROP TABLE trips`)
		_, _ = s.db.ExecContext(ctx, `ALTER TABLE trips_new RENAME TO trips`)
		_, _ = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trips_route ON trips(route_id)`)
		_, _ = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trips_service ON trips(service_id)`)
		_, _ = s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
		if rows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`); err == nil {
			_ = rows.Close()
		}
	}
	var stationSQL string
	_ = s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='stations'`).Scan(&stationSQL)
	if stationSQL != "" && !strings.Contains(stationSQL, "UNIQUE(name, region_code)") {
		_, _ = s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
		_, _ = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS stations_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL, lat REAL NOT NULL DEFAULT 0, lon REAL NOT NULL DEFAULT 0, geo_cell INTEGER,
			region_code TEXT NOT NULL DEFAULT '', timezone TEXT, quality_flags INTEGER, primary_provider TEXT,
			UNIQUE(name, region_code)
		)`)
		_, _ = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO stations_new(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider) SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations`)
		_, _ = s.db.ExecContext(ctx, `DROP TABLE stations`)
		_, _ = s.db.ExecContext(ctx, `ALTER TABLE stations_new RENAME TO stations`)
		_, _ = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_station_geo ON stations(geo_cell)`)
		_, _ = s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_stations_name_region ON stations(name, region_code)`)
		_, _ = s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	}
	return nil
}

func (s *SQLiteStore) deduplicateStations(ctx context.Context) error {
	var dups []struct{ name, region string }
	rows, err := s.db.QueryContext(ctx, `SELECT name, region_code FROM stations GROUP BY name, region_code HAVING COUNT(*) > 1`)
	if err == nil {
		for rows.Next() {
			var n, r string
			_ = rows.Scan(&n, &r)
			dups = append(dups, struct{ name, region string }{n, r})
		}
		_ = rows.Close()
	}
	for _, d := range dups {
		var keepID int64
		err := s.db.QueryRowContext(ctx, `SELECT id FROM stations WHERE name=? AND region_code=? ORDER BY CASE WHEN lat=0 AND lon=0 THEN 1 ELSE 0 END, id LIMIT 1`, d.name, d.region).Scan(&keepID)
		if err != nil {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE stops SET station_id=? WHERE station_id IN (SELECT id FROM stations WHERE name=? AND region_code=? AND id != ?)`, keepID, d.name, d.region, keepID)
		_, _ = s.db.ExecContext(ctx, `UPDATE station_codes SET station_id=? WHERE station_id IN (SELECT id FROM stations WHERE name=? AND region_code=? AND id != ?)`, keepID, d.name, d.region, keepID)
		_, _ = s.db.ExecContext(ctx, `DELETE FROM stations WHERE name=? AND region_code=? AND id != ?`, d.name, d.region, keepID)
	}
	_, _ = s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_stations_name_region ON stations(name, region_code)`)
	return nil
}

func (s *SQLiteStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO imports(provider_id, at, records, status) VALUES(?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET at=excluded.at, records=excluded.records`, providerID, at.Unix(), records, "ok")
	return err
}
func (s *SQLiteStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO imports(provider_id, at, records, status, snapshot, checksum, issues) VALUES(?,?,?,?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET at=excluded.at, records=excluded.records, snapshot=excluded.snapshot, checksum=excluded.checksum, issues=excluded.issues`, providerID, at.Unix(), records, "ok", snapshot, checksum, issues)
	return err
}
func (s *SQLiteStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	var r ImportRow
	err := s.db.QueryRowContext(ctx, `SELECT provider_id, at, records, status, snapshot, checksum, issues FROM imports WHERE provider_id=?`, providerID).Scan(&r.ProviderID, &r.At, &r.Records, &r.Status, &r.Snapshot, &r.Checksum, &r.Issues)
	if err != nil {
		return ImportRow{}, false
	}
	return r, true
}
func (s *SQLiteStore) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	status := "pending"
	if role == "admin" {
		status = "active"
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO users(email, pass_hash, status, role, created_at, config) VALUES(?,?,?,?,?,?)`, email, passHash, status, role, time.Now().Unix(), "")
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *SQLiteStore) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	var r UserRow
	err := s.db.QueryRowContext(ctx, `SELECT id, email, pass_hash, status, role, created_at, config FROM users WHERE email=?`, email).Scan(&r.ID, &r.Email, &r.PassHash, &r.Status, &r.Role, &r.CreatedAt, &r.Config)
	if err != nil {
		return UserRow{}, false
	}
	return r, true
}
func (s *SQLiteStore) GetUserByID(ctx context.Context, id int64) (UserRow, bool) {
	var r UserRow
	err := s.db.QueryRowContext(ctx, `SELECT id, email, pass_hash, status, role, created_at, config FROM users WHERE id=?`, id).Scan(&r.ID, &r.Email, &r.PassHash, &r.Status, &r.Role, &r.CreatedAt, &r.Config)
	if err != nil {
		return UserRow{}, false
	}
	return r, true
}
func (s *SQLiteStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, email, pass_hash, status, role, created_at, config FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var r UserRow
		rows.Scan(&r.ID, &r.Email, &r.PassHash, &r.Status, &r.Role, &r.CreatedAt, &r.Config)
		out = append(out, r)
	}
	return out, nil
}
func (s *SQLiteStore) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}
func (s *SQLiteStore) UpdateUserRole(ctx context.Context, id int64, role string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}
func (s *SQLiteStore) UpdateUserConfig(ctx context.Context, id int64, config string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET config=? WHERE id=?`, config, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}
func (s *SQLiteStore) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM api_keys WHERE user_id=?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_ = tx.Rollback()
		return fmt.Errorf("not found")
	}
	return tx.Commit()
}
func generateApiKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "tm_" + hex.EncodeToString(b)
}

func (s *SQLiteStore) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	key := generateApiKey()
	if scopes == "" {
		scopes = "mcp:read"
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO api_keys(user_id, key, scopes, created_at, last_used) VALUES(?,?,?,?,?)`, userID, key, scopes, time.Now().Unix(), 0)
	if err != nil {
		return ApiKeyRow{}, err
	}
	id, _ := res.LastInsertId()
	return ApiKeyRow{ID: id, UserID: userID, Key: key, Scopes: scopes, CreatedAt: time.Now().Unix()}, nil
}
func (s *SQLiteStore) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) {
	var r ApiKeyRow
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, key, scopes, created_at, last_used FROM api_keys WHERE key=?`, key).Scan(&r.ID, &r.UserID, &r.Key, &r.Scopes, &r.CreatedAt, &r.LastUsed)
	if err != nil {
		return ApiKeyRow{}, false
	}
	return r, true
}
func (s *SQLiteStore) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, key, scopes, created_at, last_used FROM api_keys WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApiKeyRow
	for rows.Next() {
		var r ApiKeyRow
		rows.Scan(&r.ID, &r.UserID, &r.Key, &r.Scopes, &r.CreatedAt, &r.LastUsed)
		out = append(out, r)
	}
	return out, nil
}
func (s *SQLiteStore) DeleteApiKey(ctx context.Context, id int64, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=? AND user_id=?`, id, userID)
	return err
}
func (s *SQLiteStore) TouchApiKey(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET last_used=? WHERE key=?`, time.Now().Unix(), key)
	return err
}
func (s *SQLiteStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, code, msg, at) VALUES(?,?,?,?,?,?,?)`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Code, q.Msg, q.At)
	return err
}
func (s *SQLiteStore) ClearQualityIssues(ctx context.Context, providerID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM quality_issues WHERE provider_id=?`, providerID)
	return err
}
func (s *SQLiteStore) ClearProviderData(ctx context.Context, providerID string) error {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM stop_times WHERE trip_id IN (SELECT id FROM trips WHERE provider_id=?)`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM transfers WHERE from_stop_id IN (SELECT id FROM stops WHERE provider_id=?) OR to_stop_id IN (SELECT id FROM stops WHERE provider_id=?)`, providerID, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM station_codes WHERE provider_id=?`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM stops WHERE provider_id=?`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM trips WHERE provider_id=?`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM routes WHERE provider_id=?`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM carriers WHERE provider_id=?`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM service_days WHERE service_id IN (SELECT id FROM services WHERE provider_id=?)`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM service_exceptions WHERE service_id IN (SELECT id FROM services WHERE provider_id=?)`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM services WHERE provider_id=?`, providerID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM stations WHERE primary_provider=?`, providerID)
	return nil
}
func (s *SQLiteStore) UpsertService(ctx context.Context, r ServiceRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO services(id, provider_id, name, start_date, end_date) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, start_date=excluded.start_date, end_date=excluded.end_date`, r.ID, r.ProviderID, r.Name, r.StartDate, r.EndDate)
	return err
}
func (s *SQLiteStore) UpsertServiceDay(ctx context.Context, r ServiceDayRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO service_days(service_id, weekday) VALUES(?,?) ON CONFLICT(service_id, weekday) DO NOTHING`, r.ServiceID, r.Weekday)
	return err
}
func (s *SQLiteStore) UpsertServiceException(ctx context.Context, r ServiceExceptionRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO service_exceptions(service_id, date, exception_type) VALUES(?,?,?) ON CONFLICT(service_id, date) DO UPDATE SET exception_type=excluded.exception_type`, r.ServiceID, r.Date, r.ExceptionType)
	return err
}
func (s *SQLiteStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }

func (s *SQLiteStore) UpsertCity(ctx context.Context, c CityRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO cities(name, region_code, lat, lon, timezone, population, kind, source) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(name, region_code) DO UPDATE SET lat=excluded.lat, lon=excluded.lon`, c.Name, c.RegionCode, c.Lat, c.Lon, c.Timezone, c.Population, c.Kind, c.Source)
	if err != nil {
		var existing int64
		if e2 := s.db.QueryRowContext(ctx, `SELECT id FROM cities WHERE name=? AND region_code=?`, c.Name, c.RegionCode).Scan(&existing); e2 == nil {
			return existing, nil
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT id FROM cities WHERE name=? AND region_code=?`, c.Name, c.RegionCode).Scan(&id)
	}
	return id, nil
}

func (s *SQLiteStore) UpsertStation(ctx context.Context, r StationRow) (int64, error) {
	if r.ID != 0 {
		_, err := s.db.ExecContext(ctx, `INSERT INTO stations(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, lat=excluded.lat, lon=excluded.lon, geo_cell=excluded.geo_cell, region_code=excluded.region_code, quality_flags=excluded.quality_flags, city_id=excluded.city_id`, r.ID, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider, r.CityID)
		return r.ID, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO stations(name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider, city_id) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(name, region_code) DO UPDATE SET lat=excluded.lat, lon=excluded.lon, geo_cell=excluded.geo_cell, quality_flags=excluded.quality_flags, city_id=excluded.city_id`, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider, r.CityID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
			var existing int64
			if e2 := s.db.QueryRowContext(ctx, `SELECT id FROM stations WHERE name=? AND region_code=?`, r.Name, r.RegionCode).Scan(&existing); e2 == nil {
				_, _ = s.db.ExecContext(ctx, `UPDATE stations SET lat=?, lon=?, geo_cell=?, quality_flags=?, primary_provider=?, city_id=? WHERE id=?`, r.Lat, r.Lon, r.GeoCell, r.QualityFlags, r.PrimaryProvider, r.CityID, existing)
				return existing, nil
			}
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		var existing int64
		_ = s.db.QueryRowContext(ctx, `SELECT id FROM stations WHERE name=? AND region_code=?`, r.Name, r.RegionCode).Scan(&existing)
		if existing != 0 {
			return existing, nil
		}
	}
	return id, nil
}
func (s *SQLiteStore) UpsertStop(ctx context.Context, r StopRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO stops(station_id, provider_id, external_code, stop_type, transport_type, name, raw_name) VALUES(?,?,?,?,?,?,?) ON CONFLICT(provider_id, external_code) DO UPDATE SET station_id=excluded.station_id, name=excluded.name`, r.StationID, r.ProviderID, r.ExternalCode, r.StopType, r.TransportType, r.Name, r.RawName)
	if err != nil {
		return 0, err
	}
	if r.ID != 0 {
		return r.ID, nil
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		var existing int64
		err = s.db.QueryRowContext(ctx, `SELECT id FROM stops WHERE provider_id=? AND external_code=?`, r.ProviderID, r.ExternalCode).Scan(&existing)
		return existing, err
	}
	return id, nil
}
func (s *SQLiteStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO station_codes(station_id, provider_id, code_type, code, name_form, address) VALUES(?,?,?,?,?,?) ON CONFLICT(station_id, provider_id, code_type) DO UPDATE SET code=excluded.code`, c.StationID, c.ProviderID, c.CodeType, c.Code, c.NameForm, c.Address)
	return err
}
func (s *SQLiteStore) UpsertCarrier(ctx context.Context, r CarrierRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO carriers(provider_id, name, code, inn, address, iata, icao, sirena) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider_id, code) DO UPDATE SET name=excluded.name, inn=excluded.inn, address=excluded.address`, r.ProviderID, r.Name, r.Code, r.INN, r.Address, r.IATA, r.ICAO, r.Sirena)
	if err != nil {
		return 0, err
	}
	if r.ID != 0 {
		return r.ID, nil
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		var existing int64
		err = s.db.QueryRowContext(ctx, `SELECT id FROM carriers WHERE provider_id=? AND code=?`, r.ProviderID, r.Code).Scan(&existing)
		return existing, err
	}
	return id, nil
}
func (s *SQLiteStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO routes(provider_id, carrier_id, external_code, short_name, long_name, mode, external_uid, ord) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider_id, external_code) DO UPDATE SET long_name=excluded.long_name`, r.ProviderID, r.CarrierID, r.ExternalCode, r.ShortName, r.LongName, r.Mode, r.ExternalUID, r.Ord)
	if err != nil {
		return 0, err
	}
	if r.ID != 0 {
		return r.ID, nil
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		var existing int64
		err = s.db.QueryRowContext(ctx, `SELECT id FROM routes WHERE provider_id=? AND external_code=?`, r.ProviderID, r.ExternalCode).Scan(&existing)
		return existing, err
	}
	return id, nil
}
func (s *SQLiteStore) UpsertTrip(ctx context.Context, r TripRow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO trips(route_id, provider_id, direction, service_days, frequency_flag, period, service_id) VALUES(?,?,?,?,?,?,?)`, r.RouteID, r.ProviderID, r.Direction, r.ServiceDays, r.FrequencyFlag, r.Period, r.ServiceID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *SQLiteStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO frequencies VALUES(?,?,?,?,?,?)`, f.TripID, f.StartMin, f.EndMin, f.HeadwayMin, f.Count, f.ExactTimes)
	return err
}
func (s *SQLiteStore) UpsertStopTime(ctx context.Context, r StopTimeRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stop_times(trip_id, stop_id, seq, arrival, departure, pickup_type, drop_off_type, dwell) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(trip_id, stop_id, seq) DO UPDATE SET arrival=excluded.arrival`, r.TripID, r.StopID, r.Seq, r.Arrival, r.Departure, r.PickupType, r.DropOffType, r.Dwell)
	return err
}
func (s *SQLiteStore) UpsertTransfer(ctx context.Context, r TransferRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO transfers(from_stop_id, to_stop_id, minutes, min_transfer_time, distance_m, within_station) VALUES(?,?,?,?,?,?) ON CONFLICT(from_stop_id, to_stop_id) DO UPDATE SET minutes=excluded.minutes`, r.FromStopID, r.ToStopID, r.Minutes, r.MinTransferTime, r.DistanceM, r.WithinStation)
	return err
}

func (s *SQLiteStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	allow := map[string]bool{}
	for _, p := range providers {
		allow[p] = true
	}
	net := model.NewNetwork()
	// stations not directly needed, but stops need lat/lon from stations
	stations := map[int64]StationRow{}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider FROM stations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r StationRow
		rows.Scan(&r.ID, &r.Name, &r.Lat, &r.Lon, &r.GeoCell, &r.RegionCode, &r.Timezone, &r.QualityFlags, &r.PrimaryProvider)
		stations[r.ID] = r
	}
	rows.Close()

	stopIDMap := map[int64]string{}
	stopRows, err := s.db.QueryContext(ctx, `SELECT id, station_id, provider_id, external_code, stop_type, transport_type, name, raw_name FROM stops`)
	if err != nil {
		return nil, err
	}
	for stopRows.Next() {
		var r StopRow
		stopRows.Scan(&r.ID, &r.StationID, &r.ProviderID, &r.ExternalCode, &r.StopType, &r.TransportType, &r.Name, &r.RawName)
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		st, ok := stations[r.StationID]
		lat, lon := 0.0, 0.0
		if ok {
			lat, lon = st.Lat, st.Lon
		}
		ms := &model.Stop{ID: r.ExternalCode, ProviderID: r.ProviderID, Name: r.Name, Lat: lat, Lon: lon, Type: model.StopType(r.StopType)}
		if ms.Type == "" {
			ms.Type = classifier.Ru.Classify(r.Name, nil)
		}
		net.Stops[r.ExternalCode] = ms
		stopIDMap[r.ID] = r.ExternalCode
	}
	stopRows.Close()

	routeRows, err := s.db.QueryContext(ctx, `SELECT id, provider_id, carrier_id, external_code, short_name, long_name, mode FROM routes`)
	if err != nil {
		return nil, err
	}
	routeIDToCode := map[int64]string{}
	routeIDToMode := map[int64]string{}
	for routeRows.Next() {
		var r RouteRow
		routeRows.Scan(&r.ID, &r.ProviderID, &r.CarrierID, &r.ExternalCode, &r.ShortName, &r.LongName, &r.Mode)
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		routeIDToCode[r.ID] = r.ExternalCode
		routeIDToMode[r.ID] = r.Mode
		net.Routes[r.ExternalCode] = &model.Route{ID: r.ExternalCode, ProviderID: r.ProviderID, ShortName: r.ShortName, LongName: r.LongName, Mode: model.Mode(r.Mode)}
	}
	routeRows.Close()

	tripRows, err := s.db.QueryContext(ctx, `SELECT id, route_id, provider_id, direction, service_id FROM trips`)
	if err != nil {
		return nil, err
	}
	trips := []TripRow{}
	for tripRows.Next() {
		var r TripRow
		tripRows.Scan(&r.ID, &r.RouteID, &r.ProviderID, &r.Direction, &r.ServiceID)
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		trips = append(trips, r)
	}
	tripRows.Close()

	tripByID := map[int64]TripRow{}
	for _, t := range trips {
		tripByID[t.ID] = t
	}
	allST, err := s.db.QueryContext(ctx, `SELECT trip_id, stop_id, seq, arrival, departure FROM stop_times ORDER BY trip_id, seq`)
	if err == nil {
		grouped := map[int64][]model.StopTime{}
		for allST.Next() {
			var tripID, stopID int64
			var seq, arr, dep int
			allST.Scan(&tripID, &stopID, &seq, &arr, &dep)
			sid, ok := stopIDMap[stopID]
			if !ok {
				continue
			}
			if _, ok := tripByID[tripID]; !ok {
				continue
			}
			grouped[tripID] = append(grouped[tripID], model.StopTime{StopID: sid, Sequence: seq, ArrivalSec: arr, DepartureSec: dep})
		}
		allST.Close()
		for _, t := range trips {
			times := grouped[t.ID]
			if len(times) == 0 {
				continue
			}
			sort.Slice(times, func(i, j int) bool { return times[i].Sequence < times[j].Sequence })
			code := routeIDToCode[t.RouteID]
			mode := routeIDToMode[t.RouteID]
			mt := &model.Trip{ID: code + ":" + t.Direction, RouteID: code, ProviderID: t.ProviderID, Mode: model.Mode(mode), ServiceID: t.ServiceID, StopTimes: times}
			net.Trips[mt.ID] = mt
		}
	}
	// transfers
	trRows, err := s.db.QueryContext(ctx, `SELECT from_stop_id, to_stop_id, minutes FROM transfers`)
	if err == nil {
		for trRows.Next() {
			var r TransferRow
			trRows.Scan(&r.FromStopID, &r.ToStopID, &r.Minutes)
			from, ok1 := stopIDMap[r.FromStopID]
			to, ok2 := stopIDMap[r.ToStopID]
			if !ok1 || !ok2 {
				continue
			}
			net.Transfers = append(net.Transfers, model.Transfer{FromStopID: from, ToStopID: to, Minutes: r.Minutes})
		}
		trRows.Close()
	}
	dayBase := time.Now().UTC().Truncate(24 * time.Hour)
	if !day.IsZero() {
		dayBase = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	}
	for _, trip := range net.Trips {
		for i := 0; i < len(trip.StopTimes)-1; i++ {
			from := trip.StopTimes[i]
			to := trip.StopTimes[i+1]
			base := dayBase
			dep := base.Add(time.Duration(from.DepartureSec) * time.Second)
			arr := base.Add(time.Duration(to.ArrivalSec) * time.Second)
			if arr.Before(dep) {
				arr = arr.Add(24 * time.Hour)
			}
			net.Connections = append(net.Connections, model.Connection{TripID: trip.ID, ProviderID: trip.ProviderID, RouteID: trip.RouteID, Mode: trip.Mode, From: from.StopID, To: to.StopID, Departure: dep, Arrival: arr})
		}
	}
	sort.Slice(net.Connections, func(i, j int) bool { return net.Connections[i].Departure.Before(net.Connections[j].Departure) })
	net.BuildIndexes()
	return net, nil
}
func (s *SQLiteStore) UpsertPlace(ctx context.Context, r PlaceRow, names map[string]string) (int64, error) {
	var id int64
	if r.ID != 0 {
		_, err := s.db.ExecContext(ctx, `INSERT INTO places(id, parent_id, admin_level, level, lat, lon, tz, valid_from, valid_to) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, admin_level=excluded.admin_level, level=excluded.level, lat=excluded.lat, lon=excluded.lon, tz=excluded.tz, valid_to=excluded.valid_to`, r.ID, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo)
		if err != nil {
			return 0, err
		}
		id = r.ID
	} else {
		res, err := s.db.ExecContext(ctx, `INSERT INTO places(parent_id, admin_level, level, lat, lon, tz, valid_from, valid_to) VALUES(?,?,?,?,?,?,?,?)`, r.ParentID, r.AdminLevel, r.Level, r.Lat, r.Lon, r.Tz, r.ValidFrom, r.ValidTo)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	}
	for lang, name := range names {
		norm := strings.ToLower(strings.TrimSpace(name))
		norm = strings.Join(strings.Fields(norm), " ")
		if _, err := s.db.ExecContext(ctx, `INSERT INTO place_names(place_id, lang, name, normalized) VALUES(?,?,?,?) ON CONFLICT(place_id, lang) DO UPDATE SET name=excluded.name, normalized=excluded.normalized`, id, lang, name, norm); err != nil {
			return 0, err
		}
	}
	if err := s.rebuildClosure(ctx, id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *SQLiteStore) rebuildClosure(ctx context.Context, root int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM place_closure WHERE descendant_id=?`, root); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,0)`, root, root); err != nil {
		return err
	}
	var parent sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT parent_id FROM places WHERE id=?`, root).Scan(&parent); err != nil {
		return err
	}
	if parent.Valid {
		rows, err := s.db.QueryContext(ctx, `SELECT ancestor_id, depth FROM place_closure WHERE descendant_id=?`, parent.Int64)
		if err != nil {
			return err
		}
		type ancRow struct {
			anc int64
			d   int
		}
		var ancestors []ancRow
		for rows.Next() {
			var anc int64
			var d int
			rows.Scan(&anc, &d)
			ancestors = append(ancestors, ancRow{anc, d})
		}
		rows.Close()
		for _, a := range ancestors {
			s.db.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,?)`, a.anc, root, a.d+1)
		}
	}
	return nil
}

func (s *SQLiteStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	var id int64
	if r.ID != 0 {
		_, err := s.db.ExecContext(ctx, `INSERT INTO terminals(id, place_id, lat, lon, tz, validity_from, validity_to, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET place_id=excluded.place_id, lat=excluded.lat, lon=excluded.lon, tz=excluded.tz, validity_from=excluded.validity_from, validity_to=excluded.validity_to`, r.ID, r.PlaceID, r.Lat, r.Lon, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt)
		if err != nil {
			return 0, err
		}
		id = r.ID
	} else {
		res, err := s.db.ExecContext(ctx, `INSERT INTO terminals(place_id, lat, lon, tz, validity_from, validity_to, osm_compatible_name, valid_from, valid_to, last_verified_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.PlaceID, r.Lat, r.Lon, r.Tz, r.ValidityFrom, r.ValidityTo, r.OsmName, r.ValidFrom, r.ValidTo, r.LastVerifiedAt)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	}
	for lang, name := range names {
		isPrimary := 1
		if _, err := s.db.ExecContext(ctx, `INSERT INTO terminal_names(terminal_id, lang, name, is_primary) VALUES(?,?,?,?) ON CONFLICT(terminal_id, lang) DO UPDATE SET name=excluded.name`, id, lang, name, isPrimary); err != nil {
			return 0, err
		}
	}
	if ru, ok := names["ru"]; ok {
		lower := strings.ToLower(ru)
		s.db.ExecContext(ctx, `UPDATE terminals SET osm_compatible_name=? WHERE id=?`, lower, id)
	}
	for _, ident := range identifiers {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO terminal_identifiers(terminal_id, system, code_type, code, is_primary) VALUES(?,?,?,?,?) ON CONFLICT(terminal_id, system, code_type) DO UPDATE SET code=excluded.code`, id, ident.System, ident.CodeType, ident.Code, 0); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (s *SQLiteStore) SaveProvenance(ctx context.Context, p model.Provenance) error {
	raw := ""
	if len(p.Raw) > 0 {
		raw = string(p.Raw)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO provenance(entity_type, entity_id, source, confidence, observed_at, raw, actor_id) VALUES(?,?,?,?,?,?,?) ON CONFLICT(entity_type, entity_id, source) DO UPDATE SET confidence=excluded.confidence, observed_at=excluded.observed_at, raw=excluded.raw`, p.EntityType, p.EntityID, p.Source, p.Confidence, p.ObservedAt.Unix(), raw, p.ActorID)
	return err
}

func (s *SQLiteStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO review_queue(entity_type, entity_id, reason, score) VALUES(?,?,?,?) ON CONFLICT(entity_type, entity_id) DO UPDATE SET reason=excluded.reason, score=excluded.score`, e.EntityType, e.EntityID, e.Reason, e.Score)
	return err
}

func (s *SQLiteStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	var id int64
	var tz sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT p.id, p.tz FROM place_closure pc JOIN places p ON p.id=pc.ancestor_id WHERE pc.descendant_id=? AND p.level=4 LIMIT 1`, placeID).Scan(&id, &tz)
	if err != nil {
		return 0, "", err
	}
	return id, tz.String, nil
}

func (s *SQLiteStore) ImportAdaptedRecords(ctx context.Context, records []model.AdaptedRecord) (int, int, error) {
	verified := 0
	queued := 0
	for _, rec := range records {
		switch rec.Kind {
		case model.AdaptedPlace:
			level := 0
			if rec.Level != nil {
				level = *rec.Level
			}
			admin := 0
			if rec.AdminLevel != nil {
				admin = *rec.AdminLevel
			}
			lat := rec.Lat
			lon := rec.Lon
			tz := rec.Tz
			validFrom := time.Now().Format("2006-01-02")
			if rec.ValidFrom != nil {
				validFrom = rec.ValidFrom.Format("2006-01-02")
			}
			var validTo *string
			if rec.ValidTo != nil {
				s := rec.ValidTo.Format("2006-01-02")
				validTo = &s
			}
			var parentID *int64
			if rec.ParentCode != "" {
				var pid int64
				if err := s.db.QueryRowContext(ctx, `SELECT place_id FROM terminal_identifiers WHERE code=? LIMIT 1`, rec.ParentCode).Scan(&pid); err == nil {
					_ = pid
				}
				_ = parentID
			}
			row := PlaceRow{AdminLevel: admin, Level: level, Lat: lat, Lon: lon, Tz: tz, ValidFrom: validFrom, ValidTo: validTo, ParentID: parentID}
			names := map[string]string{}
			if rec.NameRu != "" {
				names["ru"] = rec.NameRu
			}
			if rec.NameEn != "" {
				names["en"] = rec.NameEn
			}
			id, err := s.UpsertPlace(ctx, row, names)
			if err != nil {
				return verified, queued, fmt.Errorf("upsert place %s: %w", rec.NameRu, err)
			}
			prov := model.Provenance{EntityType: "place", EntityID: id, Source: rec.Source, Confidence: 1, ObservedAt: time.Now(), Raw: rec.Raw}
			_ = s.SaveProvenance(ctx, prov)
			verified++
		case model.AdaptedTerminal, model.AdaptedStop:
			lat := 0.0
			lon := 0.0
			if rec.Lat != nil {
				lat = *rec.Lat
			}
			if rec.Lon != nil {
				lon = *rec.Lon
			}
			tz := rec.Tz
			names := map[string]string{}
			if rec.NameRu != "" {
				names["ru"] = rec.NameRu
			}
			if rec.NameEn != "" {
				names["en"] = rec.NameEn
			}
			osm := ""
			if rec.NameRu != "" {
				osm = strings.ToLower(rec.NameRu)
			}
			row := TerminalRow{Lat: lat, Lon: lon, Tz: tz, OsmName: osm, ValidFrom: time.Now().Format("2006-01-02")}
			id, err := s.UpsertTerminal(ctx, row, names, rec.Identifiers)
			if err != nil {
				return verified, queued, fmt.Errorf("upsert terminal %s: %w", rec.NameRu, err)
			}
			_ = id
			verified++
			rawJSON, _ := json.Marshal(rec)
			_ = rawJSON
		}
	}
	return verified, queued, nil
}

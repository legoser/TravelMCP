package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"travelmcp/internal/model"
)

type SQLiteStore struct {
	db *sql.DB
}

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
	db.Exec(`PRAGMA journal_mode=WAL`)
	db.Exec(`PRAGMA synchronous=NORMAL`)
	db.Exec(`PRAGMA cache_size=-64000`)
	db.Exec(`PRAGMA temp_store=MEMORY`)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) WithTx(ctx context.Context, fn func(Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fn(s)
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

type txStore struct {
	tx     *sql.Tx
	parent *SQLiteStore
}

func (t *txStore) Close() error { return nil }
func (t *txStore) Migrate(ctx context.Context) error { return nil }
func (t *txStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	return t.parent.LoadNetwork(ctx, providers, day)
}
func (t *txStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO imports(provider_id, at, records, status) VALUES(?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET at=excluded.at, records=excluded.records`, providerID, at.Unix(), records, "ok")
	return err
}
func (t *txStore) SaveQualityIssue(ctx context.Context, q QualityRow) error { return nil }
func (t *txStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }
func (t *txStore) UpsertStation(ctx context.Context, r StationRow) (int64, error) {
	if r.ID != 0 {
		_, err := t.tx.ExecContext(ctx, `INSERT INTO stations(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, lat=excluded.lat, lon=excluded.lon`, r.ID, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider)
		return r.ID, err
	}
	res, err := t.tx.ExecContext(ctx, `INSERT INTO stations(name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider) VALUES(?,?,?,?,?,?,?,?)`, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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
	res, err := t.tx.ExecContext(ctx, `INSERT INTO carriers(provider_id, name, code, inn, address, iata, icao, sirena) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider_id, code) DO UPDATE SET name=excluded.name`, r.ProviderID, r.Name, r.Code, r.INN, r.Address, r.IATA, r.ICAO, r.Sirena)
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

func (s *SQLiteStore) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT, lat REAL, lon REAL, geo_cell INTEGER,
			region_code TEXT, timezone TEXT, quality_flags INTEGER, primary_provider TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS stops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			station_id INTEGER REFERENCES stations(id),
			provider_id TEXT, external_code TEXT, stop_type TEXT, transport_type TEXT, name TEXT, raw_name TEXT,
			UNIQUE(provider_id, external_code)
		)`,
		`CREATE TABLE IF NOT EXISTS station_codes (
			station_id INTEGER REFERENCES stations(id),
			provider_id TEXT, code_type TEXT, code TEXT, name_form TEXT, address TEXT,
			PRIMARY KEY(station_id, provider_id, code_type)
		)`,
		`CREATE TABLE IF NOT EXISTS carriers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id TEXT, name TEXT, code TEXT, inn TEXT, address TEXT, iata TEXT, icao TEXT, sirena TEXT,
			UNIQUE(provider_id, code)
		)`,
		`CREATE TABLE IF NOT EXISTS routes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id TEXT, carrier_id INTEGER REFERENCES carriers(id),
			external_code TEXT, short_name TEXT, long_name TEXT, mode TEXT, external_uid TEXT, ord INTEGER,
			UNIQUE(provider_id, external_code)
		)`,
		`CREATE TABLE IF NOT EXISTS trips (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_id INTEGER REFERENCES routes(id),
			provider_id TEXT, direction TEXT, service_days TEXT, frequency_flag INTEGER, period TEXT, service_id INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS stop_times (
			trip_id INTEGER REFERENCES trips(id),
			stop_id INTEGER REFERENCES stops(id),
			seq INTEGER, arrival INTEGER, departure INTEGER, pickup_type INTEGER, drop_off_type INTEGER, dwell INTEGER,
			PRIMARY KEY(trip_id, stop_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS transfers (
			from_stop_id INTEGER REFERENCES stops(id),
			to_stop_id INTEGER REFERENCES stops(id),
			minutes INTEGER, min_transfer_time INTEGER, distance_m INTEGER, within_station INTEGER,
			PRIMARY KEY(from_stop_id, to_stop_id)
		)`,
		`CREATE TABLE IF NOT EXISTS quality_issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider_id TEXT, entity TEXT, entity_id TEXT, level TEXT, msg TEXT, at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS imports (
			provider_id TEXT PRIMARY KEY, at INTEGER, records INTEGER, status TEXT
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
	return nil
}

func (s *SQLiteStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO imports(provider_id, at, records, status) VALUES(?,?,?,?) ON CONFLICT(provider_id) DO UPDATE SET at=excluded.at, records=excluded.records`, providerID, at.Unix(), records, "ok")
	return err
}
func (s *SQLiteStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO quality_issues(provider_id, entity, entity_id, level, msg, at) VALUES(?,?,?,?,?,?)`, q.ProviderID, q.Entity, q.EntityID, q.Level, q.Msg, q.At)
	return err
}
func (s *SQLiteStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }

func (s *SQLiteStore) UpsertStation(ctx context.Context, r StationRow) (int64, error) {
	if r.ID != 0 {
		_, err := s.db.ExecContext(ctx, `INSERT INTO stations(id, name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, lat=excluded.lat, lon=excluded.lon`, r.ID, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider)
		return r.ID, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO stations(name, lat, lon, geo_cell, region_code, timezone, quality_flags, primary_provider) VALUES(?,?,?,?,?,?,?,?)`, r.Name, r.Lat, r.Lon, r.GeoCell, r.RegionCode, r.Timezone, r.QualityFlags, r.PrimaryProvider)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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
	res, err := s.db.ExecContext(ctx, `INSERT INTO carriers(provider_id, name, code, inn, address, iata, icao, sirena) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(provider_id, code) DO UPDATE SET name=excluded.name`, r.ProviderID, r.Name, r.Code, r.INN, r.Address, r.IATA, r.ICAO, r.Sirena)
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
			ms.Type = model.InferStopType(r.Name)
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

package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"travelmcp/internal/model"
)

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
	data, err := os.ReadFile("migrations/001_phase1_places_terminals.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("migration empty")
	}
	_, err = p.pool.Exec(ctx, string(data))
	if err != nil {
		slog.Error("postgres migrate failed", "err", err)
		return err
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

func (p *PostgresStore) WithTx(ctx context.Context, fn func(Store) error) error {
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
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	return errNotImplemented
}
func (p *PostgresStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertTrip(ctx context.Context, t TripRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	return errNotImplemented
}
func (p *PostgresStore) UpsertStopTime(ctx context.Context, st StopTimeRow) error {
	return errNotImplemented
}
func (p *PostgresStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	return errNotImplemented
}
func (p *PostgresStore) UpsertFare(ctx context.Context, f FareRow) error { return errNotImplemented }
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
	return errNotImplemented
}
func (p *PostgresStore) UpsertServiceDay(ctx context.Context, d ServiceDayRow) error {
	return errNotImplemented
}
func (p *PostgresStore) UpsertServiceException(ctx context.Context, e ServiceExceptionRow) error {
	return errNotImplemented
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
	return nil, errNotImplemented
}
func (p *PostgresStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	return nil
}
func (p *PostgresStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	return nil
}
func (p *PostgresStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	return ImportRow{}, false
}
func (p *PostgresStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	return StationRow{}, false
}
func (p *PostgresStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	return StationRow{}, false
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

func (t *pgTxStore) Migrate(ctx context.Context) error                      { return nil }
func (t *pgTxStore) Close() error                                           { return nil }
func (t *pgTxStore) WithTx(ctx context.Context, fn func(Store) error) error { return fn(t) }
func (t *pgTxStore) UpsertCity(ctx context.Context, c CityRow) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	return errNotImplemented
}
func (t *pgTxStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertTrip(ctx context.Context, r TripRow) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	return errNotImplemented
}
func (t *pgTxStore) UpsertStopTime(ctx context.Context, st StopTimeRow) error {
	return errNotImplemented
}
func (t *pgTxStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	return errNotImplemented
}
func (t *pgTxStore) UpsertFare(ctx context.Context, f FareRow) error { return errNotImplemented }
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
func (t *pgTxStore) UpsertService(ctx context.Context, s ServiceRow) error { return errNotImplemented }
func (t *pgTxStore) UpsertServiceDay(ctx context.Context, d ServiceDayRow) error {
	return errNotImplemented
}
func (t *pgTxStore) UpsertServiceException(ctx context.Context, e ServiceExceptionRow) error {
	return errNotImplemented
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
	return nil
}
func (t *pgTxStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	return nil
}
func (t *pgTxStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	return ImportRow{}, false
}
func (t *pgTxStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	return StationRow{}, false
}
func (t *pgTxStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	return StationRow{}, false
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

package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"travelmcp/internal/model"
)

type PostgresStore struct {
	db  *sql.DB
	dsn string
}

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	slog.Warn("postgres store пока stub: требуется реализация pgx (фаза 2), DSN принят", "dsn", dsn)
	return &PostgresStore{dsn: dsn}, nil
}

func (p *PostgresStore) Migrate(ctx context.Context) error {
	if p.db == nil {
		slog.Warn("postgres Migrate: stub, выполните psql -f migrations/001_phase1_places_terminals.sql", "dsn", p.dsn)
		return nil
	}
	data, err := os.ReadFile("migrations/001_phase1_places_terminals.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("migration empty")
	}
	_, err = p.db.ExecContext(ctx, string(data))
	if err != nil {
		slog.Error("postgres migrate failed", "err", err)
		return err
	}
	return nil
}

func (p *PostgresStore) Close() error {
	if p.db == nil {
		return nil
	}
	return p.db.Close()
}
func (p *PostgresStore) WithTx(ctx context.Context, fn func(Store) error) error {
	if p.db == nil {
		return fn(p)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ps := &pgTxStore{tx: tx, parent: p}
	if err := fn(ps); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// --- stubs until full port (фазы 2+). Пока возвращают notImplemented, чтобы не скрывать отсутствие фич ---

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
	return errNotImplemented
}
func (p *PostgresStore) ClearQualityIssues(ctx context.Context, providerID string) error {
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
	return 0, errNotImplemented
}
func (p *PostgresStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	return 0, errNotImplemented
}
func (p *PostgresStore) SaveProvenance(ctx context.Context, pr model.Provenance) error {
	return errNotImplemented
}
func (p *PostgresStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	return errNotImplemented
}
func (p *PostgresStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	return 0, "", errNotImplemented
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
	tx     *sql.Tx
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
	return errNotImplemented
}
func (t *pgTxStore) ClearQualityIssues(ctx context.Context, providerID string) error {
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
	return 0, errNotImplemented
}
func (t *pgTxStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	return 0, errNotImplemented
}
func (t *pgTxStore) SaveProvenance(ctx context.Context, p model.Provenance) error {
	return errNotImplemented
}
func (t *pgTxStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	return errNotImplemented
}
func (t *pgTxStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	return 0, "", errNotImplemented
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

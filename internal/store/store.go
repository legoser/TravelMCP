package store

import (
	"context"
	"time"

	"travelmcp/internal/model"
)

type Store interface {
	UpsertStation(ctx context.Context, s StationRow) (int64, error)
	UpsertStop(ctx context.Context, s StopRow) (int64, error)
	UpsertStationCode(ctx context.Context, c StationCodeRow) error
	UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error)
	UpsertRoute(ctx context.Context, r RouteRow) (int64, error)
	UpsertTrip(ctx context.Context, t TripRow) (int64, error)
	UpsertFrequency(ctx context.Context, f FrequencyRow) error
	UpsertStopTime(ctx context.Context, st StopTimeRow) error
	UpsertTransfer(ctx context.Context, tr TransferRow) error
	UpsertFare(ctx context.Context, f FareRow) error
	SaveQualityIssue(ctx context.Context, q QualityRow) error

	LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error)

	MarkImported(ctx context.Context, providerID string, at time.Time, records int) error
	MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error
	GetImport(ctx context.Context, providerID string) (ImportRow, bool)
	Migrate(ctx context.Context) error
	Close() error
	WithTx(ctx context.Context, fn func(Store) error) error
	FindStation(ctx context.Context, name, region string) (StationRow, bool)
	CreateUser(ctx context.Context, email, passHash, role string) (int64, error)
	GetUserByEmail(ctx context.Context, email string) (UserRow, bool)
	GetUserByID(ctx context.Context, id int64) (UserRow, bool)
	ListUsers(ctx context.Context) ([]UserRow, error)
	UpdateUserStatus(ctx context.Context, id int64, status string) error
	UpdateUserConfig(ctx context.Context, id int64, config string) error
	CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error)
	GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool)
	ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error)
	DeleteApiKey(ctx context.Context, id int64, userID int64) error
	TouchApiKey(ctx context.Context, key string) error
}

func New(ctx context.Context, dsn string) (Store, error) {
	if dsn == "" {
		return nil, nil
	}
	if len(dsn) >= 9 && dsn[:9] == "postgres:" {
		return newPGStub(dsn)
	}
	if dsn == "memory" || dsn == ":memory:" || dsn == "sqlite://:memory:" || dsn == "file::memory:?cache=shared" {
		return NewSQLiteStore(dsn)
	}
	if len(dsn) > 7 && dsn[:7] == "sqlite:" {
		return NewSQLiteStore(dsn)
	}
	if len(dsn) > 5 && dsn[len(dsn)-3:] == ".db" {
		return NewSQLiteStore(dsn)
	}
	return NewSQLiteStore(dsn)
}

type pgStub struct{ dsn string }

func newPGStub(dsn string) (Store, error) { return &pgStub{dsn: dsn}, nil }
func (p *pgStub) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *pgStub) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *pgStub) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	return errNotImplemented
}
func (p *pgStub) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *pgStub) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *pgStub) UpsertTrip(ctx context.Context, t TripRow) (int64, error) {
	return 0, errNotImplemented
}
func (p *pgStub) UpsertFrequency(ctx context.Context, f FrequencyRow) error { return errNotImplemented }
func (p *pgStub) UpsertStopTime(ctx context.Context, st StopTimeRow) error  { return errNotImplemented }
func (p *pgStub) UpsertTransfer(ctx context.Context, tr TransferRow) error  { return errNotImplemented }
func (p *pgStub) UpsertFare(ctx context.Context, f FareRow) error           { return errNotImplemented }
func (p *pgStub) SaveQualityIssue(ctx context.Context, q QualityRow) error  { return errNotImplemented }
func (p *pgStub) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	return nil, errNotImplemented
}
func (p *pgStub) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	return nil
}
func (p *pgStub) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	return nil
}
func (p *pgStub) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	return ImportRow{}, false
}
func (p *pgStub) Migrate(ctx context.Context) error { return nil }
func (p *pgStub) Close() error                      { return nil }
func (p *pgStub) WithTx(ctx context.Context, fn func(Store) error) error { return fn(p) }
func (p *pgStub) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	return StationRow{}, false
}
func (p *pgStub) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	return 0, errNotImplemented
}
func (p *pgStub) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	return UserRow{}, false
}
func (p *pgStub) GetUserByID(ctx context.Context, id int64) (UserRow, bool) { return UserRow{}, false }
func (p *pgStub) ListUsers(ctx context.Context) ([]UserRow, error) { return nil, errNotImplemented }
func (p *pgStub) UpdateUserStatus(ctx context.Context, id int64, status string) error { return errNotImplemented }
func (p *pgStub) UpdateUserConfig(ctx context.Context, id int64, config string) error { return errNotImplemented }
func (p *pgStub) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	return ApiKeyRow{}, errNotImplemented
}
func (p *pgStub) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) { return ApiKeyRow{}, false }
func (p *pgStub) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	return nil, errNotImplemented
}
func (p *pgStub) DeleteApiKey(ctx context.Context, id int64, userID int64) error { return errNotImplemented }
func (p *pgStub) TouchApiKey(ctx context.Context, key string) error { return nil }

var errNotImplemented = errStr("postgres store not implemented: use sqlite DSN")

type errStr string

func (e errStr) Error() string { return string(e) }

package store

import (
	"context"
	"log/slog"
	"time"

	"travelmcp/internal/model"
)

type CityStore interface {
	UpsertCity(ctx context.Context, c CityRow) (int64, error)
}

type StationStore interface {
	UpsertStation(ctx context.Context, s StationRow) (int64, error)
	UpsertStationCode(ctx context.Context, c StationCodeRow) error
	FindStation(ctx context.Context, name, region string) (StationRow, bool)
	FindStationAny(ctx context.Context, name, region string) (StationRow, bool)
}

type StopStore interface {
	UpsertStop(ctx context.Context, s StopRow) (int64, error)
	UpsertStopTime(ctx context.Context, st StopTimeRow) error
	UpsertTransfer(ctx context.Context, tr TransferRow) error
}

type RouteStore interface {
	UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error)
	UpsertRoute(ctx context.Context, r RouteRow) (int64, error)
	UpsertTrip(ctx context.Context, t TripRow) (int64, error)
	UpsertFrequency(ctx context.Context, f FrequencyRow) error
}

type ServiceStore interface {
	UpsertService(ctx context.Context, s ServiceRow) error
	UpsertServiceDay(ctx context.Context, d ServiceDayRow) error
	UpsertServiceException(ctx context.Context, e ServiceExceptionRow) error
}

type PlaceStore interface {
	UpsertPlace(ctx context.Context, r PlaceRow, names map[string]string) (int64, error)
	GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error)
}

type TerminalStore interface {
	UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error)
}

type ProvenanceStore interface {
	SaveProvenance(ctx context.Context, p model.Provenance) error
	SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error
}

type QualityStore interface {
	SaveQualityIssue(ctx context.Context, q QualityRow) error
	ClearQualityIssues(ctx context.Context, providerID string) error
}

type Store interface {
	CityStore
	StationStore
	StopStore
	RouteStore
	ServiceStore
	PlaceStore
	TerminalStore
	ProvenanceStore
	QualityStore
	UpsertFare(ctx context.Context, f FareRow) error
	ImportAdaptedRecords(ctx context.Context, records []model.AdaptedRecord) (int, int, error)

	LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error)

	MarkImported(ctx context.Context, providerID string, at time.Time, records int) error
	MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error
	GetImport(ctx context.Context, providerID string) (ImportRow, bool)
	Migrate(ctx context.Context) error
	Close() error
	WithTx(ctx context.Context, fn func(Store) error) error
	ClearProviderData(ctx context.Context, providerID string) error
	CreateUser(ctx context.Context, email, passHash, role string) (int64, error)
	GetUserByEmail(ctx context.Context, email string) (UserRow, bool)
	GetUserByID(ctx context.Context, id int64) (UserRow, bool)
	ListUsers(ctx context.Context) ([]UserRow, error)
	UpdateUserStatus(ctx context.Context, id int64, status string) error
	UpdateUserRole(ctx context.Context, id int64, role string) error
	UpdateUserConfig(ctx context.Context, id int64, config string) error
	DeleteUser(ctx context.Context, id int64) error
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
		return NewPostgresStore(ctx, dsn)
	}
	if dsn == "memory" || dsn == ":memory:" || dsn == "sqlite://:memory:" || dsn == "file::memory:?cache=shared" {
		return newDeprecatedSQLiteStore(dsn)
	}
	if len(dsn) > 7 && dsn[:7] == "sqlite:" {
		return newDeprecatedSQLiteStore(dsn)
	}
	if len(dsn) > 5 && dsn[len(dsn)-3:] == ".db" {
		return newDeprecatedSQLiteStore(dsn)
	}
	return newDeprecatedSQLiteStore(dsn)
}

func newDeprecatedSQLiteStore(dsn string) (Store, error) {
	slog.Warn("sqlite store is deprecated: используйте postgres DSN, sqlite сохранён только для тестов/совместимости", "dsn", dsn)
	return NewSQLiteStore(dsn)
}

var errNotImplemented = errStr("postgres store: метод ещё не реализован (фаза 2), см. postgres.go")

type errStr string

func (e errStr) Error() string { return string(e) }

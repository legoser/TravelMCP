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
	Migrate(ctx context.Context) error
	Close() error
	WithTx(ctx context.Context, fn func(Store) error) error
	FindStation(ctx context.Context, name, region string) (StationRow, bool)
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
func (p *pgStub) Migrate(ctx context.Context) error { return nil }
func (p *pgStub) Close() error                      { return nil }
func (p *pgStub) WithTx(ctx context.Context, fn func(Store) error) error { return fn(p) }
func (p *pgStub) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	return StationRow{}, false
}

var errNotImplemented = errStr("postgres store not implemented: use sqlite DSN")

type errStr string

func (e errStr) Error() string { return string(e) }

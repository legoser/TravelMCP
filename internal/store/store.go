package store

import (
	"context"
	"fmt"
	"time"

	"travelmcp/internal/model"
)

type StopStore interface {
	UpsertStop(ctx context.Context, s StopRow) (int64, error)
	UpsertStopTime(ctx context.Context, st StopTimeRow) error
	UpsertTransfer(ctx context.Context, tr TransferRow) error
}

type RouteStore interface {
	UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error)
	UpsertRoute(ctx context.Context, r RouteRow) (int64, error)
	UpsertRouteRegion(ctx context.Context, routeID int64, region string) error
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
	ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error)
}

type ProvenanceStore interface {
	SaveProvenance(ctx context.Context, p model.Provenance) error
	SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error
}

type QualityStore interface {
	SaveQualityIssue(ctx context.Context, q QualityRow) error
	ClearQualityIssues(ctx context.Context, providerID string) error
}

type FareStore interface {
	UpsertZone(ctx context.Context, z ZoneRow) error
	UpsertFareAttribute(ctx context.Context, f FareAttributeRow) error
	UpsertFareRule(ctx context.Context, r FareRuleRow) error
	UpsertStopZone(ctx context.Context, s StopZoneRow) error
	ListZones(ctx context.Context) ([]ZoneRow, error)
	ListFareAttributes(ctx context.Context) ([]FareAttributeRow, error)
	ListFareRules(ctx context.Context) ([]FareRuleRow, error)
}

type QuotaStore interface {
	TryConsumeQuota(ctx context.Context, provider string, limit int) (bool, int, error)
	GetQuota(ctx context.Context, provider string, day time.Time) (QuotaRow, bool)
	SetQuotaLimit(ctx context.Context, provider string, limit int) error
	RecordApiCall(ctx context.Context, provider, endpoint string, cost int) error
	ListQuotas(ctx context.Context) ([]QuotaRow, error)
}

type JobStore interface {
	EnqueueJob(ctx context.Context, j JobRow) (int64, error)
	ClaimNextJob(ctx context.Context) (*JobRow, error)
	MarkJobDone(ctx context.Context, id int64) error
	MarkJobRetry(ctx context.Context, id int64, errMsg string) error
	MarkJobDead(ctx context.Context, id int64, errMsg string) error
	ListJobs(ctx context.Context, limit int) ([]JobRow, error)
}

type AuditStore interface {
	WriteAuditLog(ctx context.Context, userID *int64, action, entityType string, entityID *int64, details string) error
	ListAuditLogs(ctx context.Context, limit int) ([]AuditLogRow, error)
}

type ImportStore interface {
	ListImports(ctx context.Context, limit int) ([]ImportRow, error)
	ListImportLogs(ctx context.Context, limit int) ([]ImportLogRow, error)
}

type ImportLogRow struct {
	ID         int64
	JobID      *int64
	EntityType string
	EntityID   string
	Stage      string
	Action     string
	Confidence *float64
	DistanceM  *int
	Lev        *float64
	Source     string
	At         int64
}

type Store interface {
	StopStore
	RouteStore
	ServiceStore
	PlaceStore
	TerminalStore
	ProvenanceStore
	QualityStore
	FareStore
	QuotaStore
	JobStore
	AuditStore
	ImportStore
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

var drivers = map[string]func(context.Context, string) (Store, error){}

func Register(kind string, fn func(context.Context, string) (Store, error)) {
	drivers[kind] = fn
}

func New(ctx context.Context, dsn string) (Store, error) {
	if dsn == "" {
		return nil, nil
	}
	kind := detectKind(dsn)
	if fn, ok := drivers[kind]; ok {
		return fn(ctx, dsn)
	}
	return nil, fmt.Errorf("store driver %q not registered for dsn %q", kind, dsn)
}

func detectKind(dsn string) string {
	if len(dsn) >= 9 && dsn[:9] == "postgres:" {
		return "postgres"
	}
	if dsn == "memory-store" || dsn == "memory" {
		return "memory"
	}
	return ""
}

var errNotImplemented = errStr("postgres store: метод ещё не реализован (фаза 2), см. postgres.go")

type errStr string

func (e errStr) Error() string { return string(e) }

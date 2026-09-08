package store

import "travelmcp/internal/model"

// Deprecated: CityRow/StationRow/StationCodeRow — legacy, удалить вместе с MemoryStore legacy методами (UpsertCity/Station/StationCode, FindStation).
type CityRow struct {
	ID         int64
	Name       string
	RegionCode string
	Lat, Lon   float64
	Timezone   string
	Population int
	Kind       string
	Source     string
}

type StationRow struct {
	ID              int64
	Name            string
	Lat, Lon        float64
	GeoCell         int64
	RegionCode      string
	Timezone        string
	QualityFlags    int
	PrimaryProvider string
	CityID          *int64
}

type StationCodeRow struct {
	StationID  int64
	ProviderID string
	CodeType   string
	Code       string
	NameForm   string
	Address    string
}

type StopRow struct {
	ID            int64
	TerminalID    int64
	StationID     int64
	ProviderID    string
	ExternalCode  string
	StopType      string
	TransportType string
	Name          string
	RawName       string
	Lat           float64
	Lon           float64
}

type CarrierRow struct {
	ID         int64
	ProviderID string
	Name       string
	Code       string
	INN        string
	Address    string
	IATA       string
	ICAO       string
	Sirena     string
}

type RouteRow struct {
	ID                int64
	ProviderID        string
	CarrierID         int64
	ExternalRouteCode string
	ShortName         string
	LongName          string
	Mode              string
	ExternalUID       string
	Ord               int
	ValidTo           *string
}

type TripRow struct {
	ID               int64
	RouteID          int64
	ProviderID       string
	ExternalTripCode string
	Direction        string
	DirectionID      *int
	ServiceDays      string
	FrequencyFlag    int
	Period           string
	ServiceID        int
	DurationS        *int
	DistanceM        *int
	Method           *string
	ValidTo          *string
}

type FrequencyRow struct {
	TripID     int64
	StartMin   int
	EndMin     int
	HeadwayMin int
	Count      int
	ExactTimes int
}

type StopTimeRow struct {
	TripID        int64
	StopID        int64
	Seq           int
	Arrival       int
	Departure     int
	PickupType    int
	DropOffType   int
	Dwell         int
	IsProvisional bool
	MatchScore    *float64
	MatchMethod   string
}

type TransferRow struct {
	FromStopID      int64
	ToStopID        int64
	Minutes         int
	MinTransferTime int
	DistanceM       int
	WithinStation   int
}

type QualityRow struct {
	ProviderID string
	Entity     string
	EntityID   string
	Level      string
	Code       string
	Msg        string
	At         int64
}

type ServiceRow struct {
	ID         int
	ProviderID string
	Name       string
	StartDate  string
	EndDate    string
}

type ServiceDayRow struct {
	ServiceID int
	Weekday   int
}

type ServiceExceptionRow struct {
	ServiceID     int
	Date          string
	ExceptionType string
}

type FareRow struct {
	ProviderID string
	FromZone   string
	ToZone     string
	Amount     float64
	Currency   string
	Basis      string
}

type FareAttributeRow struct {
	FareID           string
	Price            float64
	Currency         string
	PaymentMethod    int
	Transfers        *int
	TransferDuration *int
	Basis            string
}

type FareRuleRow struct {
	FareID          string
	RouteID         int64
	OriginZone      *string
	DestinationZone *string
	ContainsZone    *string
}

type ZoneRow struct {
	ZoneID string
	NameRu string
	NameEn string
}

type StopZoneRow struct {
	StopID int64
	ZoneID string
}

type ImportRow struct {
	ProviderID string
	At         int64
	Records    int
	Status     string
	Snapshot   string
	Checksum   string
	Issues     int
}

type UserRow struct {
	ID        int64
	Email     string
	PassHash  string
	Status    string
	Role      string
	CreatedAt int64
	Config    string
}

type ApiKeyRow struct {
	ID        int64
	UserID    int64
	Key       string
	Scopes    string
	CreatedAt int64
	LastUsed  int64
}

type PlaceRow struct {
	ID         int64
	ParentID   *int64
	AdminLevel int
	Level      int
	Lat, Lon   *float64
	Tz         string
	ValidFrom  string
	ValidTo    *string
}

type TerminalRow struct {
	ID               int64
	PlaceID          *int64
	Lat, Lon         float64
	Tz               string
	ValidityFrom     *string
	ValidityTo       *string
	ValidFrom        string
	ValidTo          *string
	LastVerifiedAt   *int64
	IsLocked         bool
	Address          string
	TransportTypes   []string
	ObjectType       string
	EnrichmentStatus string
}

type TerminalAliasRow struct {
	TerminalID int64
	Alias      string
	Lang       string
	Source     string
}

type AttributeStateRow struct {
	EntityType string
	EntityID   int64
	Field      string
	Value      string
	Source     string
	Confidence float64
	Origin     string
	ActorID    *int64
	SyncRunID  *int64
}

type SyncRunRow struct {
	ID       int64
	PlanID   string
	Kind     string
	InputSHA string
	Tag      string
	State    string
	Summary  string
}

type SyncChunkRow struct {
	ID         int64
	RunID      int64
	Entity     string
	ChunkKey   string
	State      string
	PlanIDDone string
	Attempts   int
	LastError  string
}

type ProvenanceVote struct {
	Source     string
	Confidence float64
	ObservedAt int64
}

type LegacyTerminalRow struct {
	ID          int64
	NameRu      string
	NameEn      string
	Lat, Lon    float64
	Tz          string
	Identifiers []model.AdaptedIdentifier
	Votes       []ProvenanceVote
}

type SkeletonTerminalRow struct {
	ID               int64
	NameRu           string
	Lat, Lon         float64
	Settlement        string
	Transport         string
	Identifiers      []model.AdaptedIdentifier
	EnrichmentStatus string
}

type QuotaRow struct {
	Provider string
	Day      string
	Used     int
	Limit    int
	ResetAt  *string
}

type ApiCallRow struct {
	ID       int64
	Provider string
	Endpoint string
	At       int64
	Cost     int
}

type JobRow struct {
	ID        int64
	Type      string
	Payload   string
	Region    string
	State     string
	Attempts  int
	NextRun   string
	LastError string
	CreatedAt int64
}

type StagingTripRow struct {
	ID                int64
	Source            string
	ExternalRouteCode string
	ExternalTripCode  string
	IsSyntheticKey    bool
	RouteRaw          string
	Region            string
	TransportType     string
	State             string
	MatchedStopTimes  string
	UnmatchedStops    string
	RetryCount        int
}

type TripSourceRow struct {
	TripID        int64
	Source        string
	Price         *float64
	PriceCurrency *string
	ScheduleURL   *string
	DurationS     *int
	DistanceM     *int
	Method        *string
}

type OutboxEvent struct {
	ID          int64
	Aggregate   string
	AggregateID string
	Event       string
	Payload     string
}

type AuditLogRow struct {
	ID         int64
	UserID     *int64
	Action     string
	EntityType string
	EntityID   *int64
	At         int64
	Details    string
}

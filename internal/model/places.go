package model

import "time"

type Place struct {
	ID         int64      `json:"id"`
	ParentID   *int64     `json:"parent_id"`
	AdminLevel int        `json:"admin_level"`
	Level      Level      `json:"level"`
	Lat        *float64   `json:"lat,omitempty"`
	Lon        *float64   `json:"lon,omitempty"`
	Tz         string     `json:"tz"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
	IsCurrent  bool       `json:"is_current"`
	BBoxMinLat *float64   `json:"bbox_min_lat,omitempty"`
	BBoxMinLon *float64   `json:"bbox_min_lon,omitempty"`
	BBoxMaxLat *float64   `json:"bbox_max_lat,omitempty"`
	BBoxMaxLon *float64   `json:"bbox_max_lon,omitempty"`
}

type PlaceClosure struct {
	AncestorID   int64 `json:"ancestor_id"`
	DescendantID int64 `json:"descendant_id"`
	Depth        int   `json:"depth"`
}

type PlaceName struct {
	PlaceID    int64  `json:"place_id"`
	Lang       string `json:"lang"`
	Name       string `json:"name"`
	Normalized string `json:"normalized"`
}

type Terminal struct {
	ID             int64      `json:"id"`
	PlaceID        *int64     `json:"place_id"`
	Lat            float64    `json:"lat"`
	Lon            float64    `json:"lon"`
	Tz             string     `json:"tz"`
	ValidityFrom   *time.Time `json:"validity_from,omitempty"`
	ValidityTo     *time.Time `json:"validity_to,omitempty"`
	ValidFrom      time.Time  `json:"valid_from"`
	ValidTo        *time.Time `json:"valid_to,omitempty"`
	IsCurrent      bool       `json:"is_current"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
}

type TerminalName struct {
	TerminalID int64  `json:"terminal_id"`
	Lang       string `json:"lang"`
	Name       string `json:"name"`
	IsPrimary  bool   `json:"is_primary"`
}

type TerminalIdentifier struct {
	TerminalID int64  `json:"terminal_id"`
	System     string `json:"system"`
	CodeType   string `json:"code_type"`
	Code       string `json:"code"`
	IsPrimary  bool   `json:"is_primary"`
}

type StopTerminal struct {
	ID           int64      `json:"id"`
	TerminalID   int64      `json:"terminal_id"`
	Lat          *float64   `json:"lat,omitempty"`
	Lon          *float64   `json:"lon,omitempty"`
	StopType     string     `json:"stop_type"`
	ValidityFrom *time.Time `json:"validity_from,omitempty"`
	ValidityTo   *time.Time `json:"validity_to,omitempty"`
}

type Provenance struct {
	EntityType string    `json:"entity_type"`
	EntityID   int64     `json:"entity_id"`
	Source     string    `json:"source"`
	Confidence float64   `json:"confidence"`
	ObservedAt time.Time `json:"observed_at"`
	Raw        []byte    `json:"raw,omitempty"`
	ActorID    *int64    `json:"actor_id,omitempty"`
}

type ReviewQueueEntry struct {
	EntityType  string  `json:"entity_type"`
	EntityID    int64   `json:"entity_id"`
	Reason      string  `json:"reason"`
	Score       float64 `json:"score"`
	Fingerprint string  `json:"fingerprint,omitempty"`
}

type DensityClass string

const (
	DensityRural    DensityClass = "rural"
	DensitySuburban DensityClass = "suburban"
	DensityUrban    DensityClass = "urban"
	DensityMetro    DensityClass = "metro"
)

func (p Place) IsCountry() bool { return p.Level == LevelCountry }
func (p Place) IsRegion() bool  { return p.Level == LevelRegion }
func (p Place) IsCity() bool    { return p.Level == LevelCity }

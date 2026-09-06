package model

import (
	"encoding/json"
	"time"
)

type AdaptedKind string

const (
	AdaptedPlace    AdaptedKind = "place"
	AdaptedTerminal AdaptedKind = "terminal"
	AdaptedStop     AdaptedKind = "stop"
	AdaptedRoute    AdaptedKind = "route"
	AdaptedTrip     AdaptedKind = "trip"
)

type AdaptedIdentifier struct {
	System   string `json:"system"`
	CodeType string `json:"code_type"`
	Code     string `json:"code"`
}

type AdaptedRecord struct {
	Kind        AdaptedKind         `json:"kind"`
	Identifiers []AdaptedIdentifier `json:"identifiers"`
	NameRu      string              `json:"name_ru"`
	NameEn      string              `json:"name_en"`
	Lat         *float64            `json:"lat,omitempty"`
	Lon         *float64            `json:"lon,omitempty"`
	Tz          string              `json:"tz,omitempty"`
	AdminLevel  *int                `json:"admin_level,omitempty"`
	Level       *int                `json:"level,omitempty"`
	ParentCode  string              `json:"parent_code,omitempty"`
	ValidFrom   *time.Time          `json:"valid_from,omitempty"`
	ValidTo     *time.Time          `json:"valid_to,omitempty"`
	Source      string              `json:"source"`
	Raw         json.RawMessage     `json:"raw,omitempty"`
	Extra       map[string]string   `json:"extra,omitempty"`
}

func (r AdaptedRecord) HasCoords() bool {
	return r.Lat != nil && r.Lon != nil
}

func (r AdaptedRecord) PrimaryCode() string {
	if len(r.Identifiers) == 0 {
		return ""
	}
	return r.Identifiers[0].Code
}

package model

import "time"

type Coords struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Mode string

const (
	ModeWalk   Mode = "walk"
	ModeBus    Mode = "bus"
	ModeTram   Mode = "tram"
	ModeRail   Mode = "rail"
	ModeMetro  Mode = "metro"
	ModeTaxi   Mode = "taxi"
	ModeCar    Mode = "car"
	ModeFlight Mode = "flight"
)

type Stop struct {
	ID         string  `json:"id"`
	ProviderID string  `json:"provider_id"`
	Name       string  `json:"name"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
}

func (s *Stop) Coordinates() Coords {
	return Coords{Lat: s.Lat, Lon: s.Lon}
}

type Route struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	ShortName  string `json:"short_name"`
	LongName   string `json:"long_name"`
	Mode       Mode   `json:"mode"`
}

type StopTime struct {
	StopID    string
	Sequence  int
	Arrival   time.Time
	Departure time.Time
}

type Trip struct {
	ID         string
	RouteID    string
	ProviderID string
	Mode       Mode
	ServiceID  string
	StopTimes  []StopTime
}

type Transfer struct {
	FromStopID string
	ToStopID   string
	Minutes    int
}

type Connection struct {
	TripID     string
	ProviderID string
	RouteID    string
	Mode       Mode
	From       string
	To         string
	Departure  time.Time
	Arrival    time.Time
}

type Network struct {
	Stops       map[string]*Stop
	Routes      map[string]*Route
	Trips       map[string]*Trip
	Connections []Connection
	Transfers   []Transfer
}

func NewNetwork() *Network {
	return &Network{
		Stops:  map[string]*Stop{},
		Routes: map[string]*Route{},
		Trips:  map[string]*Trip{},
	}
}

type SearchParams struct {
	Departure      time.Time
	MaxTransfers   int
	AllowedModes   []Mode
	MaxWalkMinutes int
}

type CostBasis string

const (
	CostBasisEstimate CostBasis = "estimate"
	CostBasisFare     CostBasis = "fare"
)

type Cost struct {
	Has      bool      `json:"has"`
	Amount   float64   `json:"amount,omitempty"`
	Currency string    `json:"currency,omitempty"`
	Basis    CostBasis `json:"basis,omitempty"`
	Method   string    `json:"method,omitempty"`
}

type LegPoint struct {
	StopID string  `json:"stop_id,omitempty"`
	Name   string  `json:"name"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

type Leg struct {
	Mode         Mode      `json:"mode"`
	ProviderID   string    `json:"provider_id,omitempty"`
	RouteID      string    `json:"route_id,omitempty"`
	TripID       string    `json:"trip_id,omitempty"`
	From         LegPoint  `json:"from"`
	To           LegPoint  `json:"to"`
	Departure    time.Time `json:"departure"`
	Arrival      time.Time `json:"arrival"`
	SelfProvided bool      `json:"self_provided"`
	Cost         Cost      `json:"cost"`
}

type Journey struct {
	From      Coords    `json:"from"`
	To        Coords    `json:"to"`
	Departure time.Time `json:"departure"`
	Arrival   time.Time `json:"arrival"`
	Legs      []Leg     `json:"legs"`
	Transfers int       `json:"transfers"`
}

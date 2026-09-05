package model

import (
	"strings"
	"time"
)

type Coords struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Mode string

const (
	ModeWalk   Mode = "walk"
	ModeBus    Mode = "bus"
	ModeCoach  Mode = "coach"
	ModeTram   Mode = "tram"
	ModeRail   Mode = "rail"
	ModeMetro  Mode = "metro"
	ModeTaxi   Mode = "taxi"
	ModeCar    Mode = "car"
	ModeFlight Mode = "flight"
)

const (
	BusMaxSpeed    = 120.0
	TramMaxSpeed   = 120.0
	MetroMaxSpeed  = 120.0
	RailMaxSpeed   = 250.0
	FlightMaxSpeed = 1000.0
)

func MaxSpeed(mode Mode) float64 {
	switch mode {
	case ModeFlight:
		return FlightMaxSpeed
	case ModeRail:
		return RailMaxSpeed
	case ModeBus, ModeCoach, ModeTram, ModeMetro:
		return BusMaxSpeed
	default:
		return BusMaxSpeed
	}
}

func ParseTransitMode(s string) (Mode, bool) {
	switch s {
	case "bus", "BUS":
		return ModeBus, true
	case "coach", "COACH":
		return ModeBus, true
	case "tram", "TRAM":
		return ModeTram, true
	case "rail", "RAIL":
		return ModeRail, true
	case "metro", "METRO":
		return ModeMetro, true
	case "flight", "FLIGHT":
		return ModeFlight, true
	case "walk", "WALK":
		return ModeWalk, true
	default:
		return "", false
	}
}

func ParseTransitModes(csv string) []Mode {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	seen := map[Mode]bool{}
	var out []Mode
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if m, ok := ParseTransitMode(p); ok && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

type StopType string

const (
	StopTypeStation  StopType = "station"
	StopTypePlatform StopType = "platform"
	StopTypeHub      StopType = "hub"
	StopTypePOI      StopType = "poi"
	StopTypeAirport  StopType = "airport"
)

type Stop struct {
	ID         string   `json:"id"`
	ProviderID string   `json:"provider_id"`
	Name       string   `json:"name"`
	Lat        float64  `json:"lat"`
	Lon        float64  `json:"lon"`
	Type       StopType `json:"type"`
	ZoneID     string   `json:"zone_id,omitempty"`
	GeoCell    uint64   `json:"-"`
}

func (s *Stop) Coordinates() Coords {
	return Coords{Lat: s.Lat, Lon: s.Lon}
}

func (s *Stop) IsHub() bool {
	return s.Type == StopTypeHub
}

func (s *Stop) IsVillage() bool {
	return IsVillageName(s.Name)
}

func IsVillageName(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "остановочный пункт") || strings.Contains(lower, "ост. пункт") || strings.Contains(lower, "остановочный") {
		return true
	}
	if strings.Contains(lower, "поворот") || strings.Contains(lower, "пов.") {
		return true
	}
	for _, tok := range strings.Fields(lower) {
		clean := strings.Trim(tok, " \t\"'«».,;:!*")
		if clean == "оп" || clean == "оп." || clean == "о.п." || clean == "о.п" {
			return true
		}
		if strings.HasPrefix(clean, "оп") && len([]rune(clean)) <= 5 {
			if clean == "оп" || strings.HasPrefix(clean, "оп«") || strings.HasPrefix(clean, "оп\"") {
				return true
			}
		}
	}
	if strings.HasPrefix(lower, "оп ") || strings.Contains(lower, " оп ") || strings.Contains(lower, " оп«") || strings.Contains(lower, "оп «") {
		return true
	}
	if strings.Contains(lower, " пов ") || strings.HasPrefix(lower, "пов ") || strings.Contains(lower, "пов.") {
		return true
	}
	return false
}

func VillagePenaltyMinutes(name string) int {
	if IsVillageName(name) {
		return 15
	}
	return 0
}

func InferStopType(name string) StopType {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "аэропорт") {
		return StopTypeAirport
	}
	for _, t := range strings.Fields(lower) {
		if t == "ав" || t == "авт" || t == "а/в" || strings.Contains(t, "автовокзал") || strings.Contains(t, "автостанция") {
			return StopTypeHub
		}
	}
	if strings.Contains(lower, "автовокзал") || strings.Contains(lower, "автостанция") {
		return StopTypeHub
	}
	if IsVillageName(name) {
		return StopTypePlatform
	}
	if strings.Contains(lower, "вокзал") {
		return StopTypeStation
	}
	return StopTypeStation
}

// Station — каноническая физическая станция (вокзал/терминал), не привязана к провайдеру.
// Координаты принадлежат станции, timezone — IANA (напр. Asia/Novosibirsk), время в БД — UTC.
type Station struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Timezone string  `json:"timezone"`
}

// ProviderStop — представление станции у конкретного провайдера.
// Связь: provider_stops.station_id -> stations.id, provider_stops.stop_id -> stops.id.
type ProviderStop struct {
	StationID  string `json:"station_id"`
	ProviderID string `json:"provider_id"`
	Name       string `json:"name"`
	StopID     string `json:"stop_id"`
}

type Route struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	ShortName  string `json:"short_name"`
	LongName   string `json:"long_name"`
	Mode       Mode   `json:"mode"`
}

// StopTime — остановка рейса; времена — секунды от 00:00 UTC дня dayBase.
// GTFS-подход: оба поля для индексов (stop_id, departure_sec) и (trip_id, seq).
type StopTime struct {
	StopID       string
	Sequence     int
	ArrivalSec   int
	DepartureSec int
	PickupType   int `json:"pickup_type"`
	DropOffType  int `json:"drop_off_type"`
}

// Service — календарь рейсов (будни/сезон). Даты — UTC, в БД — INTEGER YYYYMMDD.
type Service struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
}

// ServiceDay — день недели сервиса; Weekday 0=Sunday..6=Saturday (как time.Weekday).
type ServiceDay struct {
	ServiceID int `json:"service_id"`
	Weekday   int `json:"weekday"`
}

type ExceptionType string

const (
	ExceptionAdded   ExceptionType = "added"
	ExceptionRemoved ExceptionType = "removed"
)

// ServiceException — исключения календаря на конкретную дату.
type ServiceException struct {
	ServiceID     int           `json:"service_id"`
	Date          time.Time     `json:"date"`
	ExceptionType ExceptionType `json:"exception_type"`
}

// Carrier — канонический перевозчик (ИНН опционально).
type Carrier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	INN  string `json:"inn,omitempty"`
}

// Trip — конкретный рейс; календарь вынесен в Service via ServiceID FK.
type Trip struct {
	ID         string
	RouteID    string
	ProviderID string
	Mode       Mode
	ServiceID  int
	StopTimes  []StopTime
}

type Transfer struct {
	FromStopID      string `json:"from_stop_id"`
	ToStopID        string `json:"to_stop_id"`
	Minutes         int    `json:"minutes"`
	MinTransferTime int    `json:"min_transfer_time"`
	DistanceM       int    `json:"distance_m"`
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
	DistanceM  int `json:"distance_m"`
}

type Zone struct {
	ID     string  `json:"id"`
	NameRu string  `json:"name_ru"`
	NameEn string  `json:"name_en"`
	Lat    float64 `json:"lat,omitempty"`
	Lon    float64 `json:"lon,omitempty"`
}

type FareAttribute struct {
	FareID   string  `json:"fare_id"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Basis    string  `json:"basis"`
}

type FareRule struct {
	FareID          string  `json:"fare_id"`
	RouteID         string  `json:"route_id"`
	OriginZone      *string `json:"origin_zone,omitempty"`
	DestinationZone *string `json:"destination_zone,omitempty"`
}

// Network — in-memory граф: канон. станции + провайдерские стопы + расписание.
// Connections — derived из StopTime (sorted), для CSA; в БД — stop_times с индексами.
// Индексы B1 строятся build-time, а не per-request.
type Network struct {
	Stops             map[string]*Stop
	Routes            map[string]*Route
	Trips             map[string]*Trip
	Stations          map[string]*Station
	ProviderStops     map[string]*ProviderStop
	Carriers          map[string]*Carrier
	Services          map[int]*Service
	ServiceDays       map[int][]ServiceDay
	ServiceExceptions map[int][]ServiceException
	Connections       []Connection
	Transfers         []Transfer
	Zones             map[string]*Zone
	FareAttributes    map[string]*FareAttribute
	FareRules         []FareRule
	StopZones         map[string]string

	TransfersByStop map[string][]Transfer `json:"-"`
	RouteStops      map[string][]string   `json:"-"`
	TripStops       map[string][]string   `json:"-"`
	MinTime         time.Time             `json:"-"`
	MaxTime         time.Time             `json:"-"`
}

func NewNetwork() *Network {
	return &Network{
		Stops:             map[string]*Stop{},
		Routes:            map[string]*Route{},
		Trips:             map[string]*Trip{},
		Stations:          map[string]*Station{},
		ProviderStops:     map[string]*ProviderStop{},
		Carriers:          map[string]*Carrier{},
		Services:          map[int]*Service{},
		ServiceDays:       map[int][]ServiceDay{},
		ServiceExceptions: map[int][]ServiceException{},
		Zones:             map[string]*Zone{},
		FareAttributes:    map[string]*FareAttribute{},
		StopZones:         map[string]string{},
		TransfersByStop:   map[string][]Transfer{},
		RouteStops:        map[string][]string{},
		TripStops:         map[string][]string{},
	}
}

func (n *Network) BuildIndexes() {
	n.TransfersByStop = make(map[string][]Transfer, len(n.Transfers))
	for _, tr := range n.Transfers {
		n.TransfersByStop[tr.FromStopID] = append(n.TransfersByStop[tr.FromStopID], tr)
	}
	n.RouteStops = make(map[string][]string)
	n.TripStops = make(map[string][]string)
	for _, trip := range n.Trips {
		ids := make([]string, len(trip.StopTimes))
		for i, st := range trip.StopTimes {
			ids[i] = st.StopID
		}
		n.TripStops[trip.ID] = ids
		n.RouteStops[trip.RouteID] = append(n.RouteStops[trip.RouteID], ids...)
	}
	if len(n.Connections) > 0 {
		n.MinTime = n.Connections[0].Departure
		n.MaxTime = n.Connections[0].Arrival
		for _, c := range n.Connections[1:] {
			if c.Departure.Before(n.MinTime) {
				n.MinTime = c.Departure
			}
			if c.Arrival.After(n.MaxTime) {
				n.MaxTime = c.Arrival
			}
		}
	}
	for _, s := range n.Stops {
		s.GeoCell = geoCell(s.Lat, s.Lon)
	}
}

func geoCell(lat, lon float64) uint64 {
	latI := int64((lat + 90) * 1e5)
	lonI := int64((lon + 180) * 1e5)
	return (uint64(latI) << 32) | uint64(uint32(lonI))
}

type Preference string

const (
	PreferenceArrival   Preference = "arrival"
	PreferenceTransfers Preference = "transfers"
)

type SearchParams struct {
	Departure      time.Time
	Arrival        *time.Time
	MaxTransfers   int
	AllowedModes   []Mode
	MaxWalkMinutes int
	AllowGap       bool
	Preference     Preference
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
	From         Coords    `json:"from"`
	To           Coords    `json:"to"`
	Departure    time.Time `json:"departure"`
	Arrival      time.Time `json:"arrival"`
	Legs         []Leg     `json:"legs"`
	Transfers    int       `json:"transfers"`
	Alternatives []Journey `json:"alternatives,omitempty"`
}

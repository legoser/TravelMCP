package model

import (
	"math"
	"strings"
	"time"
)

type Coords struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Mode string

const (
	ModeWalk    Mode = "walk"
	ModeBus     Mode = "bus"
	ModeCoach   Mode = "coach"
	ModeTram    Mode = "tram"
	ModeRail    Mode = "rail"
	ModeMetro   Mode = "subway"
	ModeTaxi    Mode = "taxi"
	ModeCar     Mode = "car"
	ModeBicycle Mode = "bicycle"
	ModeScooter Mode = "scooter"
	ModeFlight  Mode = "flight"
)

const (
	BusMaxSpeed     = 120.0
	TramMaxSpeed    = 120.0
	SubwayMaxSpeed  = 120.0
	RailMaxSpeed    = 250.0
	FlightMaxSpeed  = 1000.0
	CarMaxSpeed     = 130.0
	TaxiMaxSpeed    = 130.0
	BicycleMaxSpeed = 25.0
	ScooterMaxSpeed = 25.0
)

func MaxSpeed(mode Mode) float64 {
	switch mode {
	case ModeFlight:
		return FlightMaxSpeed
	case ModeRail:
		return RailMaxSpeed
	case ModeBus, ModeCoach, ModeTram, ModeMetro:
		return SubwayMaxSpeed
	case ModeCar, ModeTaxi:
		return CarMaxSpeed
	case ModeBicycle, ModeScooter:
		return BicycleMaxSpeed
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
	case "subway", "SUBWAY":
		return ModeMetro, true
	case "flight", "FLIGHT":
		return ModeFlight, true
	case "taxi", "TAXI":
		return ModeTaxi, true
	case "car", "CAR":
		return ModeCar, true
	case "bicycle", "BICYCLE":
		return ModeBicycle, true
	case "scooter", "SCOOTER":
		return ModeScooter, true
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
	if strings.Contains(lower, "метро") {
		return StopTypeStation
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
	// CarrierID — перевозчик маршрута (канон carriers.id): контакты
	// для fuzzy-легов (§5.4).
	CarrierID string `json:"carrier_id,omitempty"`
}

// StopTime — остановка рейса; времена — секунды от 00:00 UTC дня dayBase.
// GTFS-подход: оба поля для индексов (stop_id, departure_sec) и (trip_id, seq).
type StopTime struct {
	StopID        string
	Sequence      int
	ArrivalSec    int
	DepartureSec  int
	PickupType    int `json:"pickup_type"`
	DropOffType   int `json:"drop_off_type"`
	IsProvisional bool
	// IsFuzzy — время ориентировочное (интерполировано, источника нет):
	// лег получает пометку «время уточнять у перевозчика».
	IsFuzzy bool
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
	ServiceID int64 `json:"service_id"`
	Weekday   int   `json:"weekday"`
}

type ExceptionType string

const (
	ExceptionAdded   ExceptionType = "added"
	ExceptionRemoved ExceptionType = "removed"
)

// ServiceException — исключения календаря на конкретную дату.
type ServiceException struct {
	ServiceID     int64         `json:"service_id"`
	Date          time.Time     `json:"date"`
	ExceptionType ExceptionType `json:"exception_type"`
}

// Carrier — канонический перевозчик (ИНН опционально).
type Carrier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	INN  string `json:"inn,omitempty"`
	// Контакты для fuzzy-легов (§5.4): «время уточнять у перевозчика».
	Phone   string `json:"phone,omitempty"`
	InfoURL string `json:"info_url,omitempty"`
	Address string `json:"address,omitempty"`
}

// Trip — конкретный рейс; календарь вынесен в Service via ServiceID FK.
type Trip struct {
	ID         string
	RouteID    string
	ProviderID string
	Mode       Mode
	ServiceID  int64
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
	// Fuzzy — перегон из интерполированного времени (§5.4): лег
	// получает TimeHint «время уточнять у перевозчика».
	Fuzzy bool `json:"fuzzy,omitempty"`
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
	Services          map[int64]*Service
	ServiceDays       map[int64][]ServiceDay
	ServiceExceptions map[int64][]ServiceException
	Connections       []Connection
	Transfers         []Transfer
	Zones             map[string]*Zone
	FareAttributes    map[string]*FareAttribute
	FareRules         []FareRule
	StopZones         map[string]string

	TransfersByStop  map[string][]Transfer `json:"-"`
	ProvisionalStops map[string]bool       `json:"-"`
	RouteStops       map[string][]string   `json:"-"`
	TripStops        map[string][]string   `json:"-"`
	MinTime          time.Time             `json:"-"`
	MaxTime          time.Time             `json:"-"`
}

func NewNetwork() *Network {
	return &Network{
		Stops:             map[string]*Stop{},
		Routes:            map[string]*Route{},
		Trips:             map[string]*Trip{},
		Stations:          map[string]*Station{},
		ProviderStops:     map[string]*ProviderStop{},
		Carriers:          map[string]*Carrier{},
		Services:          map[int64]*Service{},
		ServiceDays:       map[int64][]ServiceDay{},
		ServiceExceptions: map[int64][]ServiceException{},
		Zones:             map[string]*Zone{},
		FareAttributes:    map[string]*FareAttribute{},
		StopZones:         map[string]string{},
		TransfersByStop:   map[string][]Transfer{},
		ProvisionalStops:  map[string]bool{},
		RouteStops:        map[string][]string{},
		TripStops:         map[string][]string{},
	}
}

// WalkTransferRadiusM — радиус пешей пересадки между остановками
// (типовая городская связка «вышел — перешёл»: 400м ≈ 5 мин).
const WalkTransferRadiusM = 400

// WalkTransferMinMinutes — минимальное время пешего transfer: чистое время
// по haversine на 5 км/ч плюс 2 минуты навигационного запаса.
const (
	walkMetersPerHour = 5000.0
	walkNavSlackMin   = 2
)

func WalkTransferMinutes(distM float64) int {
	return int(distM/walkMetersPerHour*60.0) + walkNavSlackMin
}

// BuildWalkTransfers — генерация пеших пересадок между близкими
// остановками сети (городские фиды не содержат transfers.txt; без них CSA
// не переходит между маршрутами и сеть распадается на изолированные
// коридоры). Радиус WalkTransferRadiusM, время — WalkTransferMinutes.
// Существующие n.Transfers не трогаются, добавляются только недостающие
// пары; обе дуги (A→B и B→A) симметричны. Provisional-стопы в пеших
// пересадках не участвуют (C-1: посадка/высадка да, пересадка нет).
func (n *Network) BuildWalkTransfers() {
	seen := make(map[string]bool, len(n.Transfers)*2)
	for _, tr := range n.Transfers {
		seen[tr.FromStopID+"|"+tr.ToStopID] = true
	}
	// transferCellDeg — ячейка пространственного индекса пересадок 0.1°
	// (~11 км): граничные пары радиуса WalkTransferRadiusM добираются
	// соседними ячейками ниже.
	const transferCellDeg = 0.1
	cell := func(lat, lon float64) (int, int) {
		return int((lat + 90) / transferCellDeg), int((lon + 180) / transferCellDeg)
	}
	byCell := map[[2]int][]string{}
	for id, s := range n.Stops {
		if n.ProvisionalStops[id] {
			continue
		}
		ci, cj := cell(s.Lat, s.Lon)
		byCell[[2]int{ci, cj}] = append(byCell[[2]int{ci, cj}], id)
	}
	add := func(a, b string, distM float64) {
		n.Transfers = append(n.Transfers, Transfer{FromStopID: a, ToStopID: b, Minutes: WalkTransferMinutes(distM)})
	}
	const cellDeg = 0.1
	for _, ids := range byCell {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := n.Stops[ids[i]], n.Stops[ids[j]]
				if a == nil || b == nil {
					continue
				}
				d := haversineKm(a.Lat, a.Lon, b.Lat, b.Lon) * 1000
				if d > WalkTransferRadiusM {
					continue
				}
				if !seen[ids[i]+"|"+ids[j]] {
					add(ids[i], ids[j], d)
					seen[ids[i]+"|"+ids[j]] = true
				}
				if !seen[ids[j]+"|"+ids[i]] {
					add(ids[j], ids[i], d)
					seen[ids[j]+"|"+ids[i]] = true
				}
			}
		}
	}
	// соседние ячейки transferCellDeg: граничные пары радиуса 400м
	for c, ids := range byCell {
		for _, off := range [][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}} {
			nc := [2]int{c[0] + off[0], c[1] + off[1]}
			nids := byCell[nc]
			for _, ida := range ids {
				a := n.Stops[ida]
				if a == nil {
					continue
				}
				for _, idb := range nids {
					b := n.Stops[idb]
					if b == nil {
						continue
					}
					d := haversineKm(a.Lat, a.Lon, b.Lat, b.Lon) * 1000
					if d > WalkTransferRadiusM {
						continue
					}
					if !seen[ida+"|"+idb] {
						add(ida, idb, d)
						seen[ida+"|"+idb] = true
					}
					if !seen[idb+"|"+ida] {
						add(idb, ida, d)
						seen[idb+"|"+ida] = true
					}
				}
			}
		}
	}
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	sa := math.Sin(dLat / 2)
	sb := math.Sin(dLon / 2)
	h := sa*sa + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*sb*sb
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

func (n *Network) BuildIndexes() {
	n.TransfersByStop = make(map[string][]Transfer, len(n.Transfers))
	for _, tr := range n.Transfers {
		n.TransfersByStop[tr.FromStopID] = append(n.TransfersByStop[tr.FromStopID], tr)
	}
	n.ProvisionalStops = make(map[string]bool)
	n.RouteStops = make(map[string][]string)
	n.TripStops = make(map[string][]string)
	for _, trip := range n.Trips {
		ids := make([]string, len(trip.StopTimes))
		for i, st := range trip.StopTimes {
			ids[i] = st.StopID
			if st.IsProvisional {
				n.ProvisionalStops[st.StopID] = true
			}
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
	// TimeHint — нестандартное время рейса (fuzzy): показываем
	// «время уточнять у перевозчика» + контакты (§5.4 fallback).
	TimeHint string `json:"time_hint,omitempty"`

	// IsTransit — пассажир сел/вышел не на конечных точках рейса
	// (sanity: From.StopID != первый StopTime трипа || To.StopID != последний).
	IsTransit bool `json:"is_transit,omitempty"`

	// Stops — все остановки, которые пассажир проедет в рамках этого лега.
	// первая точка == From, последняя == To; для пеших лег обычно пусто.
	Stops []LegPoint `json:"stops,omitempty"`
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

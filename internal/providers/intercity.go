package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
)

const IntercityID = "intercity"

// ——— JSON-схема датасета реестра (scripts/extract-minstran.py) ———

type reestrDataset struct {
	Source            string             `json:"source"`
	Snapshot          string             `json:"snapshot"`
	Regions           []string           `json:"regions"`
	Routes            []reestrRoute      `json:"routes"`
	Stops             []reestrStop       `json:"stops"`
	Services          []reestrService    `json:"services"`
	ServiceDays       []reestrServiceDay `json:"service_days"`
	ServiceExceptions []reestrException  `json:"service_exceptions"`
	Schedules         []reestrSched      `json:"schedules"`
}

type reestrService struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type reestrServiceDay struct {
	ServiceID int `json:"service_id"`
	Weekday   int `json:"weekday"`
}

type reestrException struct {
	ServiceID     int    `json:"service_id"`
	Date          string `json:"date"`
	ExceptionType string `json:"exception_type"`
}

type reestrRoute struct {
	Reg        string `json:"reg"`
	Name       string `json:"name"`
	Order      any    `json:"order"`
	Carrier    string `json:"carrier"`
	CarrierINN string `json:"carrier_inn"`
}

type reestrStop struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Region string   `json:"region"`
	OpReg  string   `json:"op_reg"`
	Lat    *float64 `json:"lat"`
	Lon    *float64 `json:"lon"`
}

type reestrSched struct {
	Route     string            `json:"route"`
	Direction string            `json:"direction"`
	ServiceID int               `json:"service_id"`
	Stops     []reestrSchedStop `json:"stops"`
}

type reestrSchedStop struct {
	Stop   string       `json:"stop"`
	Region string       `json:"region"`
	Winter *reestrBlock `json:"winter"`
	Summer *reestrBlock `json:"summer"`
}

type reestrBlock struct {
	Days   string   `json:"days"`
	Dep    []string `json:"dep"`
	Dwell  []string `json:"dwell"`
	Arr    []string `json:"arr"`
	Period any      `json:"period"`
}

// ——— Провайдер ———

type Intercity struct {
	reestrPath string
}

func NewIntercity(reestrPath string, now time.Time) *Intercity {
	return &Intercity{reestrPath: reestrPath}
}

func (p *Intercity) ID() string {
	return IntercityID
}

func (p *Intercity) Health() HealthStatus {
	ds, err := p.load()
	if err != nil {
		return HealthStatus{Up: false, LastError: err.Error()}
	}
	net := p.build(ds, time.Now())
	issues := model.ValidateNetwork(net)
	excluded := model.FilterExcludedStops(net, issues)
	return HealthStatus{Up: true, LastImportTime: time.Now(), Records: len(ds.Schedules), Issues: len(issues), ExcludedStops: len(excluded)}
}

func (p *Intercity) Network() (*model.Network, error) {
	return p.NetworkForDay(time.Now())
}

// NetworkForDay строит сеть с рейсами, привязанными к указанному дню (UTC).
func (p *Intercity) NetworkForDay(day time.Time) (*model.Network, error) {
	ds, err := p.load()
	if err != nil {
		return nil, err
	}
	return p.build(ds, day), nil
}

func (p *Intercity) load() (*reestrDataset, error) {
	raw, err := os.ReadFile(p.reestrPath)
	if err != nil {
		return nil, fmt.Errorf("intercity: read %s: %w", p.reestrPath, err)
	}
	var ds reestrDataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, fmt.Errorf("intercity: parse %s: %w", p.reestrPath, err)
	}
	return &ds, nil
}

func (p *Intercity) build(ds *reestrDataset, day time.Time) *model.Network {
	net := model.NewNetwork()

	for i := range ds.Stops {
		st := &ds.Stops[i]
		lat, lon := 0.0, 0.0
		if st.Lat != nil {
			lat = *st.Lat
		}
		if st.Lon != nil {
			lon = *st.Lon
		}
		net.Stops[st.ID] = &model.Stop{ID: st.ID, ProviderID: IntercityID, Name: st.Name, Lat: lat, Lon: lon, Type: model.InferStopType(st.Name)}
	}

	for i := range ds.Routes {
		r := &ds.Routes[i]
		net.Routes[r.Reg] = &model.Route{
			ID:         r.Reg,
			ProviderID: IntercityID,
			ShortName:  r.Reg,
			LongName:   r.Name,
			Mode:       model.ModeBus,
		}
	}

	for _, svc := range ds.Services {
		sd, _ := time.Parse("2006-01-02", svc.StartDate)
		ed, _ := time.Parse("2006-01-02", svc.EndDate)
		net.Services[svc.ID] = &model.Service{ID: svc.ID, Name: svc.Name, StartDate: sd, EndDate: ed}
	}
	for _, sd := range ds.ServiceDays {
		net.ServiceDays[sd.ServiceID] = append(net.ServiceDays[sd.ServiceID], model.ServiceDay{ServiceID: sd.ServiceID, Weekday: sd.Weekday})
	}
	for _, ex := range ds.ServiceExceptions {
		d, _ := time.Parse("2006-01-02", ex.Date)
		et := model.ExceptionType(ex.ExceptionType)
		net.ServiceExceptions[ex.ServiceID] = append(net.ServiceExceptions[ex.ServiceID], model.ServiceException{ServiceID: ex.ServiceID, Date: d, ExceptionType: et})
	}

	for _, sched := range ds.Schedules {
		if !p.serviceActive(net, sched.ServiceID, day) {
			continue
		}
		p.addSchedule(net, sched, day)
	}

	p.addTransferLinks(net)

	sort.Slice(net.Connections, func(i, j int) bool {
		return net.Connections[i].Departure.Before(net.Connections[j].Departure)
	})
	net.BuildIndexes()
	return net
}

// addTransferLinks строит пешие стыковки между географически близкими
// остановками (разные терминалы одного узла: вокзал/автостанция).
func (p *Intercity) addTransferLinks(net *model.Network) {
	const maxKm = 0.4
	for _, pair := range geo.NearbyPairs(net.Stops, maxKm) {
		a, b := pair[0], pair[1]
		d := geo.Haversine(a.Coordinates(), b.Coordinates())
		minutes := geo.WalkTimeMinutes(d)
		dist := int(d * 1000)
		net.Transfers = append(net.Transfers,
			model.Transfer{FromStopID: a.ID, ToStopID: b.ID, Minutes: minutes, MinTransferTime: minutes, DistanceM: dist},
			model.Transfer{FromStopID: b.ID, ToStopID: a.ID, Minutes: minutes, MinTransferTime: minutes, DistanceM: dist},
		)
	}
}

var timeRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})`)

func parseTimeMinutes(s string) (int, bool) {
	m := timeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return h*60 + min, true
}

const (
	periodWinter = "winter"
	periodSummer = "summer"
)

func blockOf(st reestrSchedStop, period string) *reestrBlock {
	if period == periodWinter {
		return st.Winter
	}
	return st.Summer
}

func blockHasTimes(b *reestrBlock) bool {
	for _, t := range b.Dep {
		if _, ok := parseTimeMinutes(t); ok {
			return true
		}
	}
	for _, t := range b.Arr {
		if _, ok := parseTimeMinutes(t); ok {
			return true
		}
	}
	return false
}

// pickPeriod выбирает зимний период, если в нём есть хоть одно время, иначе летний.
func pickPeriod(sched reestrSched) string {
	for _, st := range sched.Stops {
		if st.Winter != nil && blockHasTimes(st.Winter) {
			return periodWinter
		}
	}
	for _, st := range sched.Stops {
		if st.Summer != nil && blockHasTimes(st.Summer) {
			return periodSummer
		}
	}
	return ""
}

func runsCount(sched reestrSched, period string) int {
	n := 1
	for _, st := range sched.Stops {
		b := blockOf(st, period)
		if b == nil {
			continue
		}
		if l := len(b.Dep); l > n {
			n = l
		}
		if l := len(b.Arr); l > n {
			n = l
		}
	}
	return n
}

func timeAt(list []string, run int) (int, bool) {
	if run >= len(list) {
		return 0, false
	}
	return parseTimeMinutes(list[run])
}

func scheduleDays(sched reestrSched) string {
	for _, st := range sched.Stops {
		if st.Winter != nil && st.Winter.Days != "" {
			return st.Winter.Days
		}
		if st.Summer != nil && st.Summer.Days != "" {
			return st.Summer.Days
		}
	}
	return ""
}

func (p *Intercity) serviceActive(net *model.Network, serviceID int, day time.Time) bool {
	if serviceID == 0 {
		return true
	}
	svc, ok := net.Services[serviceID]
	if !ok {
		return true
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	if !svc.StartDate.IsZero() && d.Before(time.Date(svc.StartDate.Year(), svc.StartDate.Month(), svc.StartDate.Day(), 0, 0, 0, 0, time.UTC)) {
		return false
	}
	if !svc.EndDate.IsZero() && d.After(time.Date(svc.EndDate.Year(), svc.EndDate.Month(), svc.EndDate.Day(), 0, 0, 0, 0, time.UTC)) {
		return false
	}
	for _, ex := range net.ServiceExceptions[serviceID] {
		ed := time.Date(ex.Date.Year(), ex.Date.Month(), ex.Date.Day(), 0, 0, 0, 0, time.UTC)
		if ed.Equal(d) {
			if ex.ExceptionType == model.ExceptionRemoved {
				return false
			}
			if ex.ExceptionType == model.ExceptionAdded {
				return true
			}
		}
	}
	days, ok := net.ServiceDays[serviceID]
	if !ok || len(days) == 0 {
		return true
	}
	wd := int(d.Weekday())
	for _, sd := range days {
		if sd.Weekday == wd {
			return true
		}
	}
	return false
}

func (p *Intercity) addSchedule(net *model.Network, sched reestrSched, day time.Time) {
	period := pickPeriod(sched)
	if period == "" {
		return
	}
	runs := runsCount(sched, period)
	for r := 0; r < runs; r++ {
		p.addRun(net, sched, period, r, day)
	}
}

func (p *Intercity) addRun(net *model.Network, sched reestrSched, period string, run int, day time.Time) {
	dayBase := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	var (
		times   []model.StopTime
		prevEff int // minutes since dayBase
	)
	prevEff = -1
	for i := range sched.Stops {
		b := blockOf(sched.Stops[i], period)
		if b == nil {
			continue
		}
		arrMin, hasArr := timeAt(b.Arr, run)
		depMin, hasDep := timeAt(b.Dep, run)
		if !hasArr && !hasDep {
			continue
		}
		if !hasArr {
			arrMin = depMin
		}
		if !hasDep {
			depMin = arrMin
		}

		arrOff := 0
		for arrMin+arrOff*1440 < prevEff {
			arrOff++
		}
		eff := arrMin + arrOff*1440
		depOff := arrOff
		for depMin+depOff*1440 < eff {
			depOff++
		}
		effDep := depMin + depOff*1440
		prevEff = effDep

		times = append(times, model.StopTime{
			StopID:       sched.Stops[i].Stop,
			Sequence:     len(times),
			ArrivalSec:   int((dayBase.Add(time.Duration(eff) * time.Minute)).Sub(dayBase).Seconds()),
			DepartureSec: int((dayBase.Add(time.Duration(effDep) * time.Minute)).Sub(dayBase).Seconds()),
		})
	}
	if len(times) < 2 {
		return
	}

	svcID := sched.ServiceID
	if svcID == 0 {
		svcID = 1
	}
	tripID := fmt.Sprintf("%s-%s-%d", sched.Route, sched.Direction, run)
	net.Trips[tripID] = &model.Trip{
		ID:         tripID,
		RouteID:    sched.Route,
		ProviderID: IntercityID,
		Mode:       model.ModeBus,
		ServiceID:  svcID,
		StopTimes:  times,
	}
	for i := 0; i < len(times)-1; i++ {
		fromStop := net.Stops[times[i].StopID]
		toStop := net.Stops[times[i+1].StopID]
		dist := 0
		if fromStop != nil && toStop != nil {
			dist = int(geo.Haversine(fromStop.Coordinates(), toStop.Coordinates()) * 1000)
		}
		net.Connections = append(net.Connections, model.Connection{
			TripID:     tripID,
			ProviderID: IntercityID,
			RouteID:    sched.Route,
			Mode:       model.ModeBus,
			From:       times[i].StopID,
			To:         times[i+1].StopID,
			Departure:  dayBase.Add(time.Duration(times[i].DepartureSec) * time.Second),
			Arrival:    dayBase.Add(time.Duration(times[i+1].ArrivalSec) * time.Second),
			DistanceM:  dist,
		})
	}
}

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
	Source    string        `json:"source"`
	Snapshot  string        `json:"snapshot"`
	Regions   []string      `json:"regions"`
	Routes    []reestrRoute `json:"routes"`
	Stops     []reestrStop  `json:"stops"`
	Schedules []reestrSched `json:"schedules"`
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
	return HealthStatus{Up: true, LastImportTime: time.Now(), Records: len(ds.Schedules)}
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
		net.Stops[st.ID] = &model.Stop{ID: st.ID, ProviderID: IntercityID, Name: st.Name, Lat: lat, Lon: lon}
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

	for _, sched := range ds.Schedules {
		p.addSchedule(net, sched, day)
	}

	p.addTransferLinks(net)

	sort.Slice(net.Connections, func(i, j int) bool {
		return net.Connections[i].Departure.Before(net.Connections[j].Departure)
	})
	return net
}

// addTransferLinks строит пешие стыковки между географически близкими
// остановками (разные терминалы одного узла: вокзал/автостанция).
func (p *Intercity) addTransferLinks(net *model.Network) {
	const maxKm = 0.4
	for aID, a := range net.Stops {
		if a.Lat == 0 && a.Lon == 0 {
			continue
		}
		for bID, b := range net.Stops {
			if aID >= bID || (b.Lat == 0 && b.Lon == 0) {
				continue
			}
			d := geo.Haversine(a.Coordinates(), b.Coordinates())
			if d > maxKm {
				continue
			}
			minutes := geo.WalkTimeMinutes(d)
			net.Transfers = append(net.Transfers,
				model.Transfer{FromStopID: aID, ToStopID: bID, Minutes: minutes},
				model.Transfer{FromStopID: bID, ToStopID: aID, Minutes: minutes},
			)
		}
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
			StopID:    sched.Stops[i].Stop,
			Sequence:  len(times),
			Arrival:   dayBase.Add(time.Duration(eff) * time.Minute),
			Departure: dayBase.Add(time.Duration(effDep) * time.Minute),
		})
	}
	if len(times) < 2 {
		return
	}

	tripID := fmt.Sprintf("%s-%s-%d", sched.Route, sched.Direction, run)
	net.Trips[tripID] = &model.Trip{
		ID:         tripID,
		RouteID:    sched.Route,
		ProviderID: IntercityID,
		Mode:       model.ModeBus,
		ServiceID:  scheduleDays(sched),
		StopTimes:  times,
	}
	for i := 0; i < len(times)-1; i++ {
		net.Connections = append(net.Connections, model.Connection{
			TripID:     tripID,
			ProviderID: IntercityID,
			RouteID:    sched.Route,
			Mode:       model.ModeBus,
			From:       times[i].StopID,
			To:         times[i+1].StopID,
			Departure:  times[i].Departure,
			Arrival:    times[i+1].Arrival,
		})
	}
}

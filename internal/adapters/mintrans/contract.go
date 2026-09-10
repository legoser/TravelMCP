package mintrans

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var (
	reestrRegRe   = regexp.MustCompile(`^\d{2}\.\d{2}\.\d+(?:/\d+)?$`)
	reestrTimeRe  = regexp.MustCompile(`^\d{1,2}:\d{2}$`)
	reestrDwellRe = regexp.MustCompile(`^\d{1,3}(:\d{2})?$`)
	reestrDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reestrCodeRe  = regexp.MustCompile(`^\d{2}$`)
)

func ValidateDatasetContract(raw []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("reestr: неверный JSON: %w", err)
	}
	for _, k := range []string{"source", "snapshot", "routes", "stops", "schedules"} {
		if _, ok := top[k]; !ok {
			return fmt.Errorf("reestr: отсутствует обязательное поле %q", k)
		}
	}
	var ds reestrDataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return fmt.Errorf("reestr: структура не соответствует контракту: %w", err)
	}
	if ds.Source != "minstran_reestr" {
		return fmt.Errorf("reestr: source = %q, want %q", ds.Source, "minstran_reestr")
	}
	if !reestrDateRe.MatchString(ds.Snapshot) {
		return fmt.Errorf("reestr: snapshot = %q, want YYYY-MM-DD", ds.Snapshot)
	}
	if _, err := time.Parse("2006-01-02", ds.Snapshot); err != nil {
		return fmt.Errorf("reestr: snapshot = %q: %w", ds.Snapshot, err)
	}
	if err := checkRoutes(ds.Routes); err != nil {
		return err
	}
	if err := checkStops(ds.Stops); err != nil {
		return err
	}
	services := map[int64]bool{}
	for i, svc := range ds.Services {
		if svc.ID <= 0 {
			return fmt.Errorf("reestr: services[%d].id = %d, want > 0", i, svc.ID)
		}
		if services[svc.ID] {
			return fmt.Errorf("reestr: services: дубль id %d", svc.ID)
		}
		services[svc.ID] = true
		if svc.Name == "" {
			return fmt.Errorf("reestr: services[%d].name пусто", i)
		}
		for _, f := range []string{svc.StartDate, svc.EndDate} {
			if !reestrDateRe.MatchString(f) {
				return fmt.Errorf("reestr: services[%d]: дата %q, want YYYY-MM-DD", i, f)
			}
			if _, err := time.Parse("2006-01-02", f); err != nil {
				return fmt.Errorf("reestr: services[%d]: дата %q: %w", i, f, err)
			}
		}
		if svc.StartDate > svc.EndDate {
			if !wrapsYear(svc.StartDate, svc.EndDate) {
				return fmt.Errorf("reestr: services[%d]: start %q после end %q", i, svc.StartDate, svc.EndDate)
			}
		}
	}
	for i, sd := range ds.ServiceDays {
		if !services[sd.ServiceID] {
			return fmt.Errorf("reestr: service_days[%d]: service_id %d нет в services", i, sd.ServiceID)
		}
		if sd.Weekday < 0 || sd.Weekday > 6 {
			return fmt.Errorf("reestr: service_days[%d]: weekday = %d, want 0..6", i, sd.Weekday)
		}
	}
	routeSet := map[string]bool{}
	for _, r := range ds.Routes {
		routeSet[r.Reg] = true
	}
	stopSet := map[string]bool{}
	for _, st := range ds.Stops {
		stopSet[st.ID] = true
	}
	if err := checkSchedules(ds.Schedules, routeSet, stopSet, services); err != nil {
		return err
	}
	for i, c := range ds.Carriers {
		if c.Name == "" {
			return fmt.Errorf("reestr: carriers[%d].name пусто", i)
		}
	}
	return nil
}

func wrapsYear(start, end string) bool {
	s, err := time.Parse("2006-01-02", start)
	if err != nil {
		return false
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil {
		return false
	}
	return s.Month() >= time.July && e.Month() <= time.July
}

func checkRoutes(routes []reestrRoute) error {
	seen := map[string]bool{}
	for i, r := range routes {
		if !reestrRegRe.MatchString(r.Reg) {
			return fmt.Errorf("reestr: routes[%d].reg = %q, want NN.NN.NNN с опциональным суффиксом /N", i, r.Reg)
		}
		if seen[r.Reg] {
			return fmt.Errorf("reestr: routes: дубль reg %q", r.Reg)
		}
		seen[r.Reg] = true
		if r.Name == "" {
			return fmt.Errorf("reestr: routes[%d] (%s): name пусто", i, r.Reg)
		}
		if r.Order != nil {
			if _, ok := r.Order.(float64); !ok {
				return fmt.Errorf("reestr: routes[%d] (%s): order типа %T, want число или null", i, r.Reg, r.Order)
			}
		}
	}
	return nil
}

func checkStops(stops []reestrStop) error {
	seen := map[string]bool{}
	for i, st := range stops {
		if st.ID == "" {
			return fmt.Errorf("reestr: stops[%d].id пусто", i)
		}
		if seen[st.ID] {
			return fmt.Errorf("reestr: stops: дубль id %q", st.ID)
		}
		seen[st.ID] = true
		if st.Name == "" {
			return fmt.Errorf("reestr: stops[%d] (%s): name пусто", i, st.ID)
		}
		if !reestrCodeRe.MatchString(st.Region) {
			return fmt.Errorf("reestr: stops[%d] (%s): region = %q, want NN", i, st.ID, st.Region)
		}
		if st.OpReg == "" {
			return fmt.Errorf("reestr: stops[%d] (%s): op_reg пусто", i, st.ID)
		}
		if st.Lat != nil && (*st.Lat < -90 || *st.Lat > 90) {
			return fmt.Errorf("reestr: stops[%d] (%s): lat = %v, want -90..90", i, st.ID, *st.Lat)
		}
		if st.Lon != nil && (*st.Lon < -180 || *st.Lon > 180) {
			return fmt.Errorf("reestr: stops[%d] (%s): lon = %v, want -180..180", i, st.ID, *st.Lon)
		}
	}
	return nil
}

func blockDaysOf(b *reestrBlock) string {
	if b == nil {
		return ""
	}
	return b.Days
}

func checkSchedules(scheds []reestrSched, routeSet, stopSet map[string]bool, services map[int64]bool) error {
	seenDir := map[string]bool{}
	for i, sc := range scheds {
		if sc.Route == "" {
			return fmt.Errorf("reestr: schedules[%d].route пусто", i)
		}
		if !routeSet[sc.Route] {
			return fmt.Errorf("reestr: schedules[%d]: route %q нет в routes", i, sc.Route)
		}
		if sc.Direction != "forward" && sc.Direction != "backward" {
			return fmt.Errorf("reestr: schedules[%d] (%s): direction = %q, want forward|backward", i, sc.Route, sc.Direction)
		}
		k := sc.Route + "|" + sc.Direction
		if seenDir[k] {
			return fmt.Errorf("reestr: schedules: дубль %q", k)
		}
		seenDir[k] = true
		if sc.ServiceID <= 0 {
			return fmt.Errorf("reestr: schedules[%d] (%s): service_id = %d, want > 0", i, sc.Route, sc.ServiceID)
		}
		if len(services) > 0 && !services[sc.ServiceID] {
			return fmt.Errorf("reestr: schedules[%d] (%s): service_id %d нет в services", i, sc.Route, sc.ServiceID)
		}
		if len(sc.Stops) == 0 {
			return fmt.Errorf("reestr: schedules[%d] (%s): stops пусто", i, sc.Route)
		}
		for j, s := range sc.Stops {
			if s.Stop == "" {
				return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d].stop пусто", i, sc.Route, j)
			}
			if !stopSet[s.Stop] {
				return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d]: stop %q нет в stops", i, sc.Route, j, s.Stop)
			}
			if !reestrCodeRe.MatchString(s.Region) {
				return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d]: region = %q, want NN", i, sc.Route, j, s.Region)
			}
			for _, bb := range []struct {
				name string
				b    *reestrBlock
			}{{"winter", s.Winter}, {"summer", s.Summer}} {
				if bb.b == nil {
					continue
				}
				if _, err := ParseBlockDays(blockDaysOf(bb.b)); err != nil {
					return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d].%s.days: %w", i, sc.Route, j, bb.name, err)
				}
				for _, t := range bb.b.Dep {
					if t == "" || IsNoServiceCell(t) {
						continue
					}
					if _, _, _, err := ParseCellTime(t); err != nil {
						return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d].%s.dep = %q: %w", i, sc.Route, j, bb.name, t, err)
					}
				}
				for _, t := range bb.b.Arr {
					if t == "" || IsNoServiceCell(t) {
						continue
					}
					if _, _, _, err := ParseCellTime(t); err != nil {
						return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d].%s.arr = %q: %w", i, sc.Route, j, bb.name, t, err)
					}
				}
				for _, t := range bb.b.Dwell {
					if t == "" {
						continue
					}
					if !reestrDwellRe.MatchString(t) {
						if _, _, _, err := ParseCellTime(t); err != nil {
							return fmt.Errorf("reestr: schedules[%d] (%s): stops[%d].%s.dwell = %q, want M или H:MM: %w", i, sc.Route, j, bb.name, t, err)
						}
					}
				}
			}
		}
	}
	return nil
}

// ——— JSON-схема датасета реестра (scripts/extract-minstran.py) ———

type reestrDataset struct {
	Source    string          `json:"source"`
	Snapshot  string          `json:"snapshot"`
	Routes    []reestrRoute   `json:"routes"`
	Stops     []reestrStop    `json:"stops"`
	Carriers  []reestrCarrier `json:"carriers"`
	Schedules []reestrSched   `json:"schedules"`
	Services  []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	} `json:"services"`
	ServiceDays []struct {
		ServiceID int64 `json:"service_id"`
		Weekday   int   `json:"weekday"`
	} `json:"service_days"`
	ServiceExceptions []struct {
		ServiceID     int64  `json:"service_id"`
		Date          string `json:"date"`
		ExceptionType string `json:"exception_type"`
	} `json:"service_exceptions"`
}

type reestrCarrier struct {
	Name    string `json:"name"`
	INN     string `json:"inn"`
	OGRN    string `json:"ogrn"`
	Address string `json:"address"`
	Email   string `json:"email"`
}
type reestrRoute struct {
	Reg            string `json:"reg"`
	Name           string `json:"name"`
	Order          any    `json:"order"`
	Carrier        string `json:"carrier"`
	CarrierINN     string `json:"carrier_inn"`
	CarrierOGRN    string `json:"carrier_ogrn"`
	CarrierAddress string `json:"carrier_address"`
	CarrierEmail   string `json:"carrier_email"`
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
	ServiceID int64             `json:"service_id"`
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

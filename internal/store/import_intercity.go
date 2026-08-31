package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
)

type reestrDataset struct {
	Source    string        `json:"source"`
	Snapshot  string        `json:"snapshot"`
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

func ImportIntercity(ctx context.Context, s Store, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ds reestrDataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return err
	}
	if err := s.Migrate(ctx); err != nil {
		return err
	}
	// Station grouping: stops with same coords within 0.4km -> same station
	type stationKey struct{ id int64 }
	stations := map[string]int64{}
	stopToStation := map[string]int64{}
	var stationRows []StationRow

	for _, st := range ds.Stops {
		lat, lon := 0.0, 0.0
		if st.Lat != nil {
			lat = *st.Lat
		}
		if st.Lon != nil {
			lon = *st.Lon
		}
		found := int64(0)
		for _, sr := range stationRows {
			if sr.Lat == 0 && sr.Lon == 0 || lat == 0 && lon == 0 {
				continue
			}
			d := geo.Haversine(model.Coords{Lat: lat, Lon: lon}, model.Coords{Lat: sr.Lat, Lon: sr.Lon})
			if d < 0.4 {
				found = sr.ID
				break
			}
		}
		if found == 0 {
			sr := StationRow{Name: st.Name, Lat: lat, Lon: lon, RegionCode: st.Region, PrimaryProvider: "intercity"}
			if lat == 0 && lon == 0 {
				if nlat, nlon, ok := geocodeStation(st.Name, st.Region); ok {
					sr.Lat, sr.Lon = nlat, nlon
				} else {
					sr.QualityFlags = 1
				}
			}
			id, _ := s.UpsertStation(ctx, sr)
			// memory store allocs ID inside; need to capture actual ID
			// For memory store, Upsert returns allocated ID; we need to store it
			// Use returned id as station id
			sr.ID = id
			stationRows = append(stationRows, sr)
			stations[st.ID] = id
			stopToStation[st.ID] = id
			_ = stationKey{}
		} else {
			stopToStation[st.ID] = found
		}
	}

	// carriers
	carrierMap := map[string]int64{}
	for _, r := range ds.Routes {
		key := r.Carrier + "|" + r.CarrierINN
		if _, ok := carrierMap[key]; ok {
			continue
		}
		id, _ := s.UpsertCarrier(ctx, CarrierRow{ProviderID: "intercity", Name: r.Carrier, Code: r.CarrierINN, INN: r.CarrierINN})
		carrierMap[key] = id
	}

	// stops
	stopIDMap := map[string]int64{}
	for _, st := range ds.Stops {
		stationID := stopToStation[st.ID]
		sr := StopRow{StationID: stationID, ProviderID: "intercity", ExternalCode: st.ID, StopType: string(model.InferStopType(st.Name)), Name: st.Name, RawName: st.Name}
		id, _ := s.UpsertStop(ctx, sr)
		stopIDMap[st.ID] = id
		_ = s.UpsertStationCode(ctx, StationCodeRow{StationID: stationID, ProviderID: "intercity", CodeType: "op_reg", Code: st.OpReg, NameForm: st.Name})
	}

	// routes
	routeIDMap := map[string]int64{}
	for _, r := range ds.Routes {
		carrierKey := r.Carrier + "|" + r.CarrierINN
		cid := carrierMap[carrierKey]
		rr := RouteRow{ProviderID: "intercity", CarrierID: cid, ExternalCode: r.Reg, ShortName: r.Reg, LongName: r.Name, Mode: string(model.ModeBus)}
		id, _ := s.UpsertRoute(ctx, rr)
		routeIDMap[r.Reg] = id
	}

	// trips + stopTimes: разворачиваем все runs как в providers/intercity.go
	for _, sched := range ds.Schedules {
		routeID := routeIDMap[sched.Route]
		period := pickPeriod(sched)
		if period == "" {
			continue
		}
		runs := runsCount(sched, period)
		for run := 0; run < runs; run++ {
			times := []struct {
				stopID string
				arrMin int
				depMin int
			}{}
			prevEff := -1
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
				times = append(times, struct {
					stopID string
					arrMin int
					depMin int
				}{stopID: sched.Stops[i].Stop, arrMin: eff, depMin: effDep})
			}
			if len(times) < 2 {
				continue
			}
			tr := TripRow{RouteID: routeID, ProviderID: "intercity", Direction: sched.Direction, ServiceID: sched.ServiceID}
			tid, _ := s.UpsertTrip(ctx, tr)
			for seq, tm := range times {
				sid, ok := stopIDMap[tm.stopID]
				if !ok {
					continue
				}
				_ = s.UpsertStopTime(ctx, StopTimeRow{TripID: tid, StopID: sid, Seq: seq, Arrival: tm.arrMin * 60, Departure: tm.depMin * 60})
			}
		}
	}

	if ms, ok := s.(*MemoryStore); ok {
		_ = ms
		// add transfers between stations within 0.4km (already grouped, but keep as within_station transfers)
		for i := 0; i < len(stationRows); i++ {
			for j := i + 1; j < len(stationRows); j++ {
				a := stationRows[i]
				b := stationRows[j]
				if a.Lat == 0 || b.Lat == 0 {
					continue
				}
				d := geo.Haversine(model.Coords{Lat: a.Lat, Lon: a.Lon}, model.Coords{Lat: b.Lat, Lon: b.Lon})
				if d < 0.4 {
					// find any stop per station
					var from, to int64
					for sid, stID := range stopToStation {
						if stID == a.ID {
							from = stopIDMap[sid]
							break
						}
					}
					for sid, stID := range stopToStation {
						if stID == b.ID {
							to = stopIDMap[sid]
							break
						}
					}
					if from != 0 && to != 0 {
						_ = s.UpsertTransfer(ctx, TransferRow{FromStopID: from, ToStopID: to, Minutes: geo.WalkTimeMinutes(d), WithinStation: 1})
						_ = s.UpsertTransfer(ctx, TransferRow{FromStopID: to, ToStopID: from, Minutes: geo.WalkTimeMinutes(d), WithinStation: 1})
					}
				}
			}
		}
	}

	return s.MarkImported(ctx, "intercity", time.Now(), len(ds.Routes))
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

func parseHM(s string) int {
	if v, ok := parseTimeMinutes(s); ok {
		return v
	}
	return 0
}
func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func blockOf(st reestrSchedStop, period string) *reestrBlock {
	if period == "winter" {
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
func pickPeriod(sched reestrSched) string {
	for _, st := range sched.Stops {
		if st.Winter != nil && blockHasTimes(st.Winter) {
			return "winter"
		}
	}
	for _, st := range sched.Stops {
		if st.Summer != nil && blockHasTimes(st.Summer) {
			return "summer"
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

var regionNames = map[string]string{
	"22": "Алтайский край", "04": "Республика Алтай", "42": "Кемеровская область",
	"54": "Новосибирская область", "70": "Томская область", "24": "Красноярский край",
	"19": "Республика Хакасия", "17": "Республика Тыва", "86": "Ханты-Мансийский АО",
}

func geocodeStation(name, region string) (float64, float64, bool) {
	key := os.Getenv("YANDEX_GEOCODE_KEY")
	if key == "" {
		if data, err := os.ReadFile(".env"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "YANDEX_GEOCODE_KEY=") {
					key = strings.Trim(strings.SplitN(line, "=", 2)[1], "\"' ")
					break
				}
			}
		}
	}
	if key == "" {
		return 0, 0, false
	}
	regionName := regionNames[region]
	q := fmt.Sprintf("Россия, %s, %s", regionName, name)
	u := "https://geocode-maps.yandex.ru/1.x/?format=json&results=1&apikey=" + url.QueryEscape(key) + "&geocode=" + url.QueryEscape(q)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	var body struct {
		Response struct {
			GeoObjectCollection struct {
				FeatureMember []struct {
					GeoObject struct {
						Point struct {
							Pos string `json:"pos"`
						} `json:"Point"`
					} `json:"GeoObject"`
				} `json:"featureMember"`
			} `json:"GeoObjectCollection"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, 0, false
	}
	if len(body.Response.GeoObjectCollection.FeatureMember) == 0 {
		return 0, 0, false
	}
	pos := body.Response.GeoObjectCollection.FeatureMember[0].GeoObject.Point.Pos
	parts := strings.Split(pos, " ")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var lon, lat float64
	fmt.Sscan(parts[0], &lon)
	fmt.Sscan(parts[1], &lat)
	if lat == 0 && lon == 0 {
		return 0, 0, false
	}
	return lat, lon, true
}

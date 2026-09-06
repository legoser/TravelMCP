// Package importer — импорт реестра Минтранса в Store (фаза 2, SRP).

package mintrans

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/store"
	"travelmcp/internal/support/classifier"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
)

type GeoResolver interface {
	Resolve(ctx context.Context, name, region string) (float64, float64, string, float64, bool)
}

func dedupDistanceM() float64 {
	cfg, _ := config.Load("")
	if cfg != nil && cfg.Deduplication.DistanceM > 0 {
		return float64(cfg.Deduplication.DistanceM) / 1000.0
	}
	return 0.2
}

type reestrDataset struct {
	Source    string          `json:"source"`
	Snapshot  string          `json:"snapshot"`
	Routes    []reestrRoute   `json:"routes"`
	Stops     []reestrStop    `json:"stops"`
	Carriers  []reestrCarrier `json:"carriers"`
	Schedules []reestrSched   `json:"schedules"`
	Services  []struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	} `json:"services"`
	ServiceDays []struct {
		ServiceID int `json:"service_id"`
		Weekday   int `json:"weekday"`
	} `json:"service_days"`
	ServiceExceptions []struct {
		ServiceID     int    `json:"service_id"`
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

func ImportIntercity(ctx context.Context, s store.Store, path string, logger *slog.Logger) error {
	return ImportIntercityWithResolver(ctx, s, path, logger, nil)
}

func ImportIntercityWithResolver(ctx context.Context, s store.Store, path string, logger *slog.Logger, resolver GeoResolver) error {
	if logger == nil {
		logger = slog.Default()
	}
	t0 := time.Now()
	jobID := int64(0)
	if pg, ok := s.(interface {
		LogImportEntry(ctx context.Context, jobID int64, entityType, entityID, stage, action string, confidence float64, distanceM int, lev float64, source string) error
	}); ok {
		_ = pg.LogImportEntry(ctx, 0, "job", "intercity", "normalize", "import_start", 0, 0, 0, "mintrans")
		_ = jobID
	}
	logger.Info("import reading dataset", "path", path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ds reestrDataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])
	snapshot := ds.Snapshot
	if snapshot == "" {
		snapshot = time.Now().Format("2006-01-02")
	}
	if os.Getenv("FORCE_IMPORT") != "1" && os.Getenv("FORCE_GEOCODE") != "1" {
		if imp, ok := s.GetImport(ctx, "intercity"); ok && imp.Checksum == checksum {
			logger.Info("import skipped - same checksum", "snapshot", snapshot, "checksum", checksum[:8])
			return nil
		}
	}
	logger.Info("dataset loaded", "routes", len(ds.Routes), "stops", len(ds.Stops), "schedules", len(ds.Schedules), "snapshot", snapshot, "checksum", checksum[:8], "elapsed_ms", time.Since(t0).Milliseconds())
	if err := s.Migrate(ctx); err != nil {
		return err
	}
	type resolved struct {
		lat, lon float64
		source   string
		conf     float64
		ok       bool
	}
	resolvedByName := map[string]resolved{}
	if resolver != nil {
		uniqueKeys := map[string]string{}
		for _, st := range ds.Stops {
			if st.Lat != nil && st.Lon != nil {
				continue
			}
			key := st.Name + "|" + st.Region
			if _, ok := uniqueKeys[key]; !ok {
				uniqueKeys[key] = st.Name
			}
		}
		if len(uniqueKeys) > 0 {
			logger.Info("geocoding missing coords", "unique", len(uniqueKeys))
			for key, name := range uniqueKeys {
				region := strings.SplitN(key, "|", 2)[1]
				lat, lon, src, conf, ok := resolver.Resolve(ctx, name, region)
				if ok {
					resolvedByName[key] = resolved{lat: lat, lon: lon, source: src, conf: conf, ok: true}
					logger.Debug("geocoded", "name", name, "region", region, "lat", lat, "lon", lon, "source", src, "conf", conf)
				} else {
					resolvedByName[key] = resolved{ok: false}
					logger.Debug("geocode not found", "name", name, "region", region)
				}
			}
			logger.Info("geocoding done", "resolved", len(resolvedByName))
		}
	}
	// Use transaction for batch insert - Store.WithTx hides sqlite details
	var importErr error
	importErr = s.WithTx(ctx, func(tx store.Store) error {
		_ = tx.ClearQualityIssues(ctx, "intercity")
		_ = tx.ClearProviderData(ctx, "intercity")
		logger.Info("terminal grouping started (canonical)")
		stopToTerminal := map[string]int64{}
		var terminalRows []store.TerminalRow
		terminalByID := map[int64]store.TerminalRow{}

		dedupDist := dedupDistanceM()
		for _, st := range ds.Stops {
			lat, lon := 0.0, 0.0
			if st.Lat != nil {
				lat = *st.Lat
			}
			if st.Lon != nil {
				lon = *st.Lon
			}
			resSrc, resConf, resOk := "", 0.0, false
			if lat == 0 && lon == 0 && resolver != nil {
				key := st.Name + "|" + st.Region
				if r, ok := resolvedByName[key]; ok && r.ok {
					lat, lon = r.lat, r.lon
					resSrc, resConf, resOk = r.source, r.conf, true
				}
			}
			found := int64(0)
			for _, tr := range terminalRows {
				if tr.Lat == 0 && tr.Lon == 0 || lat == 0 && lon == 0 {
					continue
				}
				d := geo.Haversine(model.Coords{Lat: lat, Lon: lon}, model.Coords{Lat: tr.Lat, Lon: tr.Lon})
				if d < dedupDist {
					found = tr.ID
					break
				}
			}
			if found == 0 {
				tr := store.TerminalRow{Lat: lat, Lon: lon, Tz: "", ValidFrom: "", ValidTo: nil}
				id, _ := tx.UpsertTerminal(ctx, tr, map[string]string{"ru": st.Name}, []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: st.OpReg}})
				tr.ID = id
				terminalRows = append(terminalRows, tr)
				terminalByID[id] = tr
				stopToTerminal[st.ID] = id
				if resOk {
					raw := []byte(fmt.Sprintf(`{"geocoder":%q,"similarity":%.3f}`, resSrc, resConf))
					_ = tx.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: id, Source: "mintrans", Confidence: resConf, ObservedAt: time.Now(), Raw: raw})
					_ = tx.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: id, Reason: "low_confidence", Score: resConf})
				} else if lat == 0 && lon == 0 {
					_ = tx.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: id, Reason: "missing_coords", Score: 0.3})
				}
			} else {
				stopToTerminal[st.ID] = found
			}
		}
		logger.Info("terminals grouped", "count", len(terminalRows), "elapsed_ms", time.Since(t0).Milliseconds())

		// carriers: приоритет ds.Carriers (полные данные из листа Перевозчики), fallback - маршруты
		carrierMap := map[string]int64{}
		if len(ds.Carriers) > 0 {
			for _, c := range ds.Carriers {
				key := c.Name + "|" + c.INN
				if _, ok := carrierMap[key]; ok {
					continue
				}
				id, _ := tx.UpsertCarrier(ctx, store.CarrierRow{ProviderID: "intercity", Name: c.Name, Code: c.INN, INN: c.INN, Address: c.Address})
				carrierMap[key] = id
			}
		}
		for _, r := range ds.Routes {
			key := r.Carrier + "|" + r.CarrierINN
			if _, ok := carrierMap[key]; ok {
				continue
			}
			addr := r.CarrierAddress
			in := r.CarrierINN
			if len(ds.Carriers) == 0 {
				// fallback when dataset without carriers section
				addr = ""
			}
			id, _ := tx.UpsertCarrier(ctx, store.CarrierRow{ProviderID: "intercity", Name: r.Carrier, Code: in, INN: in, Address: addr})
			carrierMap[key] = id
		}
		logger.Info("carriers ready", "count", len(carrierMap), "elapsed_ms", time.Since(t0).Milliseconds())

		// services (canonical calendar)
		for _, svc := range ds.Services {
			_ = tx.UpsertService(ctx, store.ServiceRow{ID: svc.ID, ProviderID: "intercity", Name: svc.Name, StartDate: svc.StartDate, EndDate: svc.EndDate})
		}
		for _, sd := range ds.ServiceDays {
			_ = tx.UpsertServiceDay(ctx, store.ServiceDayRow{ServiceID: sd.ServiceID, Weekday: sd.Weekday})
		}
		for _, ex := range ds.ServiceExceptions {
			_ = tx.UpsertServiceException(ctx, store.ServiceExceptionRow{ServiceID: ex.ServiceID, Date: ex.Date, ExceptionType: ex.ExceptionType})
		}
		if len(ds.Services) > 0 {
			logger.Info("services ready", "count", len(ds.Services), "days", len(ds.ServiceDays))
		}

		// stops_canonical
		stopIDMap := map[string]int64{}
		for _, st := range ds.Stops {
			terminalID := stopToTerminal[st.ID]
			lat, lon := 0.0, 0.0
			if st.Lat != nil {
				lat = *st.Lat
			}
			if st.Lon != nil {
				lon = *st.Lon
			}
			// if terminal has coords, use them for stop geom fallback
			if tr, ok := terminalByID[terminalID]; ok && (lat == 0 && lon == 0) {
				lat, lon = tr.Lat, tr.Lon
			}
			sr := store.StopRow{TerminalID: terminalID, Lat: lat, Lon: lon, StopType: string(classifier.Ru.Classify(st.Name, nil)), Name: st.Name}
			id, _ := tx.UpsertStop(ctx, sr)
			stopIDMap[st.ID] = id
		}
		logger.Info("stops ready", "count", len(stopIDMap), "elapsed_ms", time.Since(t0).Milliseconds())

		// routes
		routeIDMap := map[string]int64{}
		for _, r := range ds.Routes {
			carrierKey := r.Carrier + "|" + r.CarrierINN
			cid := carrierMap[carrierKey]
			rr := store.RouteRow{ProviderID: "intercity", CarrierID: cid, ExternalRouteCode: r.Reg, ShortName: r.Reg, LongName: r.Name, Mode: string(model.ModeBus)}
			id, _ := tx.UpsertRoute(ctx, rr)
			routeIDMap[r.Reg] = id
			primaryRegion := ""
			if len(r.Reg) >= 2 {
				primaryRegion = r.Reg[:2]
			}
			if primaryRegion != "" {
				_ = tx.UpsertRouteRegion(ctx, id, primaryRegion)
			}
		}
		for _, sc := range ds.Schedules {
			if rid, ok := routeIDMap[sc.Route]; ok {
				seen := map[string]bool{}
				for _, sst := range sc.Stops {
					if sst.Region != "" && !seen[sst.Region] {
						seen[sst.Region] = true
						_ = tx.UpsertRouteRegion(ctx, rid, sst.Region)
					}
				}
			}
		}
		logger.Info("routes ready", "count", len(routeIDMap), "elapsed_ms", time.Since(t0).Milliseconds())

		tripStart := time.Now()
		type pendingTrip struct {
			row   store.TripRow
			times []struct {
				stopID string
				arrMin int
				depMin int
			}
		}
		var pending []pendingTrip
		var mu sync.Mutex
		numCPU := runtime.NumCPU()
		sem := make(chan struct{}, numCPU*2)
		var wg sync.WaitGroup
		for _, sc := range ds.Schedules {
			routeID := routeIDMap[sc.Route]
			period := pickPeriod(sc)
			if period == "" {
				continue
			}
			runs := runsCount(sc, period)
			for run := 0; run < runs; run++ {
				wg.Add(1)
				sem <- struct{}{}
				go func(sched reestrSched, routeID int64, period string, run int) {
					defer wg.Done()
					defer func() { <-sem }()
					times := []struct {
						stopID string
						arrMin int
						depMin int
					}{}
					prevEff := -1
					for i := range sched.Stops {
						if len(times) > 0 {
							prevTerminal := stopToTerminal[times[len(times)-1].stopID]
							curTerminal := stopToTerminal[sched.Stops[i].Stop]
							if prevTerminal != 0 && curTerminal != 0 && prevTerminal == curTerminal {
								continue
							}
						}
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
						return
					}
					mu.Lock()
					// У реестра нет кода трипа: детерминированный synthetic NK
					// (направление + service + прогон), NOT NULL по схеме.
					code := fmt.Sprintf("synthetic:%s:%d:%d", sched.Direction, sched.ServiceID, run)
					pending = append(pending, pendingTrip{row: store.TripRow{RouteID: routeID, ProviderID: "intercity", ExternalTripCode: code, Direction: sched.Direction, ServiceID: sched.ServiceID}, times: times})
					mu.Unlock()
				}(sc, routeID, period, run)
			}
		}
		wg.Wait()
		sort.Slice(pending, func(i, j int) bool { return pending[i].row.RouteID < pending[j].row.RouteID })
		logger.Info("trips prepared", "count", len(pending), "workers", numCPU, "elapsed_ms", time.Since(tripStart).Milliseconds())
		for _, pt := range pending {
			tid, _ := tx.UpsertTrip(ctx, pt.row)
			for seq, tm := range pt.times {
				sid, ok := stopIDMap[tm.stopID]
				if !ok {
					continue
				}
				_ = tx.UpsertStopTime(ctx, store.StopTimeRow{TripID: tid, StopID: sid, Seq: seq, Arrival: tm.arrMin * 60, Departure: tm.depMin * 60})
			}
		}
		logger.Info("trips stored", "elapsed_ms", time.Since(tripStart).Milliseconds())

		// transfers: пешие стыковки <0.2км между терминалами с координатами (canonical)
		terminalToStop := map[int64]int64{}
		for sid, tID := range stopToTerminal {
			if _, ok := terminalToStop[tID]; !ok {
				if stopID, ok2 := stopIDMap[sid]; ok2 {
					terminalToStop[tID] = stopID
				}
			}
		}
		// dedupDist уже вычислен выше, переиспользуем
		for i := 0; i < len(terminalRows); i++ {
			for j := i + 1; j < len(terminalRows); j++ {
				a := terminalRows[i]
				b := terminalRows[j]
				if a.Lat == 0 || b.Lat == 0 {
					continue
				}
				d := geo.Haversine(model.Coords{Lat: a.Lat, Lon: a.Lon}, model.Coords{Lat: b.Lat, Lon: b.Lon})
				if d < dedupDist {
					from := terminalToStop[a.ID]
					to := terminalToStop[b.ID]
					if from != 0 && to != 0 {
						distM := int(d * 1000)
						minutes := geo.WalkTimeMinutes(d)
						_ = tx.UpsertTransfer(ctx, store.TransferRow{FromStopID: from, ToStopID: to, Minutes: minutes, MinTransferTime: minutes, DistanceM: distM, WithinStation: 1})
						_ = tx.UpsertTransfer(ctx, store.TransferRow{FromStopID: to, ToStopID: from, Minutes: minutes, MinTransferTime: minutes, DistanceM: distM, WithinStation: 1})
					}
				}
			}
		}
		logger.Info("transfers ready", "elapsed_ms", time.Since(t0).Milliseconds())
		return nil
	})
	if importErr != nil {
		return importErr
	}
	// Quality analysis - post-commit (очищено в Tx, пишем заново)
	issues := 0
	if net, err := s.LoadNetwork(ctx, []string{"intercity"}, time.Now()); err == nil {
		vals := model.ValidateNetwork(net)
		issues = len(vals)
		_ = s.ClearQualityIssues(ctx, "intercity")
		for _, iss := range vals {
			_ = s.SaveQualityIssue(ctx, store.QualityRow{ProviderID: iss.ProviderID, Entity: iss.Entity, EntityID: iss.Entity, Level: string(iss.Level), Code: iss.Code, Msg: iss.Message, At: time.Now().Unix()})
		}
		logger.Info("quality analyzed", "issues", issues)
	}
	if err := s.MarkImportedVersion(ctx, "intercity", snapshot, checksum, time.Now(), len(ds.Routes), issues); err != nil {
		return s.MarkImported(ctx, "intercity", time.Now(), len(ds.Routes))
	}
	return nil
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

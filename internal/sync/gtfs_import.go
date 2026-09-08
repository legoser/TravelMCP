package sync

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

// GtfsImportStore — узкий интерфейс канала импорта GTFS-фида в канон.
// GTFS-стопы становятся терминалами (identifiers gtfs:stop_code) со своими
// stops_canonical; routes/trips/service напрямую по NK (источник 'gtfs'),
// attach-конвейер не участвует: фид несёт собственные stop_id→geo-связки.
type GtfsImportStore interface {
	UpsertTerminal(ctx context.Context, r store.TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error)
	EnsureStopForTerminal(ctx context.Context, terminalID int64, lat, lon float64, name string) (int64, error)
	FindRouteID(ctx context.Context, source, routeCode string) (int64, bool)
	UpsertRoute(ctx context.Context, r store.RouteRow) (int64, error)
	FindTrip(ctx context.Context, routeID int64, tripCode string) (store.TripRow, bool)
	UpsertTrip(ctx context.Context, t store.TripRow) (int64, error)
	UpsertService(ctx context.Context, s store.ServiceRow) error
	UpsertServiceDay(ctx context.Context, d store.ServiceDayRow) error
	UpsertServiceException(ctx context.Context, e store.ServiceExceptionRow) error
	UpsertFrequency(ctx context.Context, f store.FrequencyRow) error
	AppendStopTimes(ctx context.Context, rows []store.StopTimeRow) error
	ListTerminalIDByCode(ctx context.Context, system, code string) (int64, bool)
	CreateSyncRun(ctx context.Context, r store.SyncRunRow) (int64, error)
	FinishSyncRun(ctx context.Context, id int64, state, summary string) error
}

// GtfsServiceOffset разводит пространства id сервисов: services.id — bigserial
// общий, GTFS-фиды нумеруют service_id произвольно (SPb: 169888+).
const GtfsServiceOffset = int64(10_000_000_000)

type GtfsImportStats struct {
	Stops       int   `json:"stops"`
	Routes      int   `json:"routes"`
	Services    int   `json:"services"`
	Trips       int   `json:"trips"`
	StopTimes   int   `json:"stop_times"`
	Frequencies int   `json:"frequencies"`
	Exceptions  int   `json:"exceptions"`
	ElapsedMs   int64 `json:"elapsed_ms"`
}

// ImportGtfsFeed — конвейер импорта: stops → services(+days/exceptions) →
// routes → trips → frequencies → stop_times (стрим, батчи). Идемпотентен:
// все upsert по NK, повторный прогон воскрешает (valid_to=NULL).
func ImportGtfsFeed(ctx context.Context, st GtfsImportStore, zr *zip.Reader) (GtfsImportStats, error) {
	var stats GtfsImportStats
	start := time.Now()
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	stopIDs, err := importGtfsStops(ctx, st, files, &stats)
	if err != nil {
		return stats, err
	}
	if err := importGtfsServices(ctx, st, files, &stats); err != nil {
		return stats, err
	}
	routeIDs, err := importGtfsRoutes(ctx, st, files, &stats)
	if err != nil {
		return stats, err
	}
	tripIDs, err := importGtfsTrips(ctx, st, files, routeIDs, &stats)
	if err != nil {
		return stats, err
	}
	if err := importGtfsFrequencies(ctx, st, files, tripIDs, &stats); err != nil {
		return stats, err
	}
	if err := importGtfsStopTimes(ctx, st, files, tripIDs, stopIDs, &stats); err != nil {
		return stats, err
	}
	stats.ElapsedMs = time.Since(start).Milliseconds()
	return stats, nil
}

func gtfsOpenCSV(files map[string]*zip.File, name string) (*csv.Reader, io.Closer, error) {
	zf, ok := files[name]
	if !ok {
		return nil, nil, fmt.Errorf("gtfs: missing %s", name)
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, nil, err
	}
	r := csv.NewReader(rc)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	return r, rc, nil
}

func gtfsHeader(rows []string) map[string]int {
	m := map[string]int{}
	for i, h := range rows {
		m[strings.TrimSpace(h)] = i
	}
	return m
}

func gtfsCol(row []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i < len(row) {
		return row[i]
	}
	return ""
}

func importGtfsStops(ctx context.Context, st GtfsImportStore, files map[string]*zip.File, stats *GtfsImportStats) (map[string]int64, error) {
	out := map[string]int64{}
	r, closer, err := gtfsOpenCSV(files, "stops.txt")
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := gtfsHeader(header)
	start := time.Now()
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		sid := strings.TrimSpace(gtfsCol(row, idx, "stop_id"))
		if sid == "" {
			continue
		}
		code := strings.TrimSpace(gtfsCol(row, idx, "stop_code"))
		if code == "" {
			code = sid
		}
		name := gtfsCol(row, idx, "stop_name")
		lat := gtfsParseFloat(gtfsCol(row, idx, "stop_lat"))
		lon := gtfsParseFloat(gtfsCol(row, idx, "stop_lon"))
		transport := strings.TrimSpace(gtfsCol(row, idx, "transport_type"))
		if transport == "" {
			transport = "bus"
		}
		if id, ok := st.ListTerminalIDByCode(ctx, "gtfs", code); ok {
			stopID, err := st.EnsureStopForTerminal(ctx, id, lat, lon, name)
			if err != nil {
				return nil, fmt.Errorf("gtfs stop-canonical %s: %w", sid, err)
			}
			out[sid] = stopID
			continue
		}
		latP, lonP := lat, lon
		termID, err := st.UpsertTerminal(ctx, store.TerminalRow{
			Lat: latP, Lon: lonP,
			TransportTypes:   []string{transport},
			ObjectType:       "stop",
			EnrichmentStatus: "identity_only",
		}, map[string]string{"ru": name}, []model.AdaptedIdentifier{
			{System: "gtfs", CodeType: "gtfs_stop_id", Code: code},
		})
		if err != nil {
			return nil, fmt.Errorf("gtfs stop %s: %w", sid, err)
		}
		stopID, err := st.EnsureStopForTerminal(ctx, termID, lat, lon, name)
		if err != nil {
			return nil, fmt.Errorf("gtfs stop-canonical %s: %w", sid, err)
		}
		out[sid] = stopID
		stats.Stops++
	}
	slog.Info("gtfs import: stops", "count", stats.Stops, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return out, nil
}

func importGtfsServices(ctx context.Context, st GtfsImportStore, files map[string]*zip.File, stats *GtfsImportStats) error {
	r, closer, err := gtfsOpenCSV(files, "calendar.txt")
	if err != nil {
		return err
	}
	defer closer.Close()
	header, err := r.Read()
	if err != nil {
		return err
	}
	idx := gtfsHeader(header)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		sid, err := strconv.ParseInt(strings.TrimSpace(gtfsCol(row, idx, "service_id")), 10, 64)
		if err != nil {
			continue
		}
		svc := store.ServiceRow{ID: GtfsServiceOffset + sid, ProviderID: "gtfs", Name: gtfsCol(row, idx, "service_name"),
			StartDate: gtfsDate(gtfsCol(row, idx, "start_date")), EndDate: gtfsDate(gtfsCol(row, idx, "end_date"))}
		if err := st.UpsertService(ctx, svc); err != nil {
			return err
		}
		for _, wd := range gtfsWeekdays(gtfsCol(row, idx, "monday"), gtfsCol(row, idx, "tuesday"), gtfsCol(row, idx, "wednesday"),
			gtfsCol(row, idx, "thursday"), gtfsCol(row, idx, "friday"), gtfsCol(row, idx, "saturday"), gtfsCol(row, idx, "sunday")) {
			if err := st.UpsertServiceDay(ctx, store.ServiceDayRow{ServiceID: GtfsServiceOffset + sid, Weekday: wd}); err != nil {
				return err
			}
		}
		stats.Services++
	}
	if seR, seC, err := gtfsOpenCSV(files, "calendar_dates.txt"); err == nil {
		defer seC.Close()
		if hdr, err := seR.Read(); err == nil {
			seIdx := gtfsHeader(hdr)
			for {
				row, err := seR.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
				sid, err := strconv.ParseInt(strings.TrimSpace(gtfsCol(row, seIdx, "service_id")), 10, 64)
				if err != nil {
					continue
				}
				typ := "added"
				if strings.TrimSpace(gtfsCol(row, seIdx, "exception_type")) == "2" {
					typ = "removed"
				}
				if err := st.UpsertServiceException(ctx, store.ServiceExceptionRow{
					ServiceID: GtfsServiceOffset + sid, Date: gtfsDate(gtfsCol(row, seIdx, "date")), ExceptionType: typ,
				}); err != nil {
					return err
				}
				stats.Exceptions++
			}
		}
	}
	slog.Info("gtfs import: services", "services", stats.Services, "exceptions", stats.Exceptions)
	return nil
}

func importGtfsRoutes(ctx context.Context, st GtfsImportStore, files map[string]*zip.File, stats *GtfsImportStats) (map[string]int64, error) {
	out := map[string]int64{}
	r, closer, err := gtfsOpenCSV(files, "routes.txt")
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := gtfsHeader(header)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rid := strings.TrimSpace(gtfsCol(row, idx, "route_id"))
		if rid == "" {
			continue
		}
		if id, ok := st.FindRouteID(ctx, "gtfs", rid); ok {
			out[rid] = id
			continue
		}
		id, err := st.UpsertRoute(ctx, store.RouteRow{
			ProviderID: "gtfs", ExternalRouteCode: rid,
			ShortName: gtfsCol(row, idx, "route_short_name"),
			LongName:  gtfsCol(row, idx, "route_long_name"),
			Mode:      gtfsMode(gtfsCol(row, idx, "route_type")),
		})
		if err != nil {
			return nil, fmt.Errorf("gtfs route %s: %w", rid, err)
		}
		out[rid] = id
		stats.Routes++
	}
	slog.Info("gtfs import: routes", "count", stats.Routes)
	return out, nil
}

func importGtfsTrips(ctx context.Context, st GtfsImportStore, files map[string]*zip.File, routeIDs map[string]int64, stats *GtfsImportStats) (map[string]int64, error) {
	out := map[string]int64{}
	r, closer, err := gtfsOpenCSV(files, "trips.txt")
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := gtfsHeader(header)
	svcDays := map[int64]string{}
	start := time.Now()
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		tid := strings.TrimSpace(gtfsCol(row, idx, "trip_id"))
		rid := strings.TrimSpace(gtfsCol(row, idx, "route_id"))
		if tid == "" {
			continue
		}
		routeID, ok := routeIDs[rid]
		if !ok {
			continue
		}
		svcID, err := strconv.ParseInt(strings.TrimSpace(gtfsCol(row, idx, "service_id")), 10, 64)
		if err != nil {
			continue
		}
		directionID := gtfsParseInt(gtfsCol(row, idx, "direction_id"))
		direction := "forward"
		if directionID == 1 {
			direction = "backward"
		}
		days, ok := svcDays[svcID]
		if !ok {
			days = gtfsServiceDaysString(ctx, st, GtfsServiceOffset+svcID)
			svcDays[svcID] = days
		}
		row_ := store.TripRow{
			RouteID: routeID, ProviderID: "gtfs", ExternalTripCode: tid,
			Direction: direction, DirectionID: &directionID,
			ServiceID: GtfsServiceOffset + svcID, ServiceDays: days,
		}
		if existing, ok := st.FindTrip(ctx, routeID, tid); ok {
			out[tid] = existing.ID
			continue
		}
		id, err := st.UpsertTrip(ctx, row_)
		if err != nil {
			return nil, fmt.Errorf("gtfs trip %s: %w", tid, err)
		}
		out[tid] = id
		stats.Trips++
	}
	slog.Info("gtfs import: trips", "count", stats.Trips, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return out, nil
}

func importGtfsFrequencies(ctx context.Context, st GtfsImportStore, files map[string]*zip.File, tripIDs map[string]int64, stats *GtfsImportStats) error {
	r, closer, err := gtfsOpenCSV(files, "frequencies.txt")
	if err != nil {
		return nil
	}
	defer closer.Close()
	header, err := r.Read()
	if err != nil {
		return nil
	}
	idx := gtfsHeader(header)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		tid := strings.TrimSpace(gtfsCol(row, idx, "trip_id"))
		dbID, ok := tripIDs[tid]
		if !ok {
			continue
		}
		if err := st.UpsertFrequency(ctx, store.FrequencyRow{
			TripID:     dbID,
			StartMin:   gtfsHMSSec(gtfsCol(row, idx, "start_time")) / 60,
			EndMin:     gtfsHMSSec(gtfsCol(row, idx, "end_time")) / 60,
			HeadwayMin: gtfsParseInt(gtfsCol(row, idx, "headway_secs")) / 60,
			ExactTimes: gtfsParseInt(gtfsCol(row, idx, "exact_times")),
		}); err != nil {
			return err
		}
		stats.Frequencies++
	}
	slog.Info("gtfs import: frequencies", "count", stats.Frequencies)
	return nil
}

const gtfsStopTimesBatch = 2000

func importGtfsStopTimes(ctx context.Context, st GtfsImportStore, files map[string]*zip.File, tripIDs, stopIDs map[string]int64, stats *GtfsImportStats) error {
	r, closer, err := gtfsOpenCSV(files, "stop_times.txt")
	if err != nil {
		return err
	}
	defer closer.Close()
	header, err := r.Read()
	if err != nil {
		return err
	}
	idx := gtfsHeader(header)
	batch := make([]store.StopTimeRow, 0, gtfsStopTimesBatch)
	start := time.Now()
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		tid := strings.TrimSpace(gtfsCol(row, idx, "trip_id"))
		sid := strings.TrimSpace(gtfsCol(row, idx, "stop_id"))
		dbTrip, ok := tripIDs[tid]
		if !ok {
			continue
		}
		stopID, ok := stopIDs[sid]
		if !ok {
			continue
		}
		batch = append(batch, store.StopTimeRow{
			TripID: dbTrip, StopID: stopID,
			Seq:       gtfsParseInt(gtfsCol(row, idx, "stop_sequence")),
			Arrival:   gtfsHMSSec(gtfsCol(row, idx, "arrival_time")),
			Departure: gtfsHMSSec(gtfsCol(row, idx, "departure_time")),
		})
		if len(batch) >= gtfsStopTimesBatch {
			if err := st.AppendStopTimes(ctx, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
		stats.StopTimes++
		if stats.StopTimes%500_000 == 0 {
			slog.Info("gtfs import: stop_times progress", "done", stats.StopTimes, "elapsed", time.Since(start).Round(time.Second).String())
		}
	}
	if len(batch) > 0 {
		if err := st.AppendStopTimes(ctx, batch); err != nil {
			return err
		}
	}
	slog.Info("gtfs import: stop_times", "count", stats.StopTimes, "elapsed", time.Since(start).Round(time.Second).String())
	return nil
}

func gtfsServiceDaysString(ctx context.Context, st GtfsImportStore, svcID int64) string {
	// дни читаются из только что записанного service_days (импорт календаря
	// идёт раньше трипов); ReadServiceDays — узкое чтение для days-строки
	if rd, ok := st.(GtfsServiceDaysReader); ok {
		if days, err := rd.ReadServiceDays(ctx, svcID); err == nil && len(days) > 0 {
			parts := make([]string, 0, len(days))
			for _, d := range days {
				parts = append(parts, strconv.Itoa(d))
			}
			return strings.Join(parts, ",")
		}
	}
	return ""
}

// GtfsServiceDaysReader — опциональное чтение service_days (дни для
// trips.service_days — денормализованная строка для планировщика).
type GtfsServiceDaysReader interface {
	ReadServiceDays(ctx context.Context, serviceID int64) ([]int, error)
}

func gtfsWeekdays(mo, tu, we, th, fr, sa, su string) []int {
	out := []int{}
	for _, p := range []struct {
		v string
		d int
	}{{mo, 1}, {tu, 2}, {we, 3}, {th, 4}, {fr, 5}, {sa, 6}, {su, 0}} {
		if p.v == "1" {
			out = append(out, p.d)
		}
	}
	return out
}

func gtfsDate(s string) string {
	if len(s) == 8 {
		return s[0:4] + "-" + s[4:6] + "-" + s[6:8]
	}
	return ""
}

func gtfsMode(t string) string {
	switch strings.TrimSpace(t) {
	case "0":
		return "tram"
	case "1", "2":
		return "rail"
	case "4":
		return "flight"
	default:
		return "bus"
	}
}

func gtfsHMSSec(s string) int {
	var h, m, sec int
	if n, _ := fmt.Sscanf(strings.TrimSpace(s), "%d:%d:%d", &h, &m, &sec); n >= 2 {
		return h*3600 + m*60 + sec
	}
	return 0
}

func gtfsParseFloat(s string) float64 {
	var f float64
	fmt.Sscan(strings.TrimSpace(s), &f)
	return f
}

func gtfsParseInt(s string) int {
	var n int
	fmt.Sscan(strings.TrimSpace(s), &n)
	return n
}

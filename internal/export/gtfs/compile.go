package gtfs

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/pricing"
)

func gtfsRouteType(m model.Mode) string {
	switch m {
	case model.ModeTram:
		return "0"
	case model.ModeMetro:
		return "1"
	case model.ModeRail:
		return "2"
	case model.ModeBus, model.ModeCoach:
		return "3"
	case model.ModeFlight:
		return "1100"
	default:
		return "3"
	}
}

func writeCSV(w *zip.Writer, name string, header []string, rows [][]string) error {
	fw, err := w.Create(name)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(fw)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(r); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func Compile(net *model.Network) ([]byte, error) {
	slog.Info("gtfs compile: start", "stops", len(net.Stops), "routes", len(net.Routes), "trips", len(net.Trips), "fares", len(net.FareAttributes))
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	carriers := make([]*model.Carrier, 0, len(net.Carriers))
	for _, c := range net.Carriers {
		carriers = append(carriers, c)
	}
	sort.Slice(carriers, func(i, j int) bool { return carriers[i].ID < carriers[j].ID })
	if len(carriers) == 0 {
		carriers = append(carriers, &model.Carrier{ID: "0", Name: "Неизвестный перевозчик"})
	}
	var agencyRows [][]string
	for _, c := range carriers {
		agencyRows = append(agencyRows, []string{c.ID, c.Name, "https://travelmcp.local", "Europe/Moscow"})
	}
	if err := writeCSV(zw, "agency.txt", []string{"agency_id", "agency_name", "agency_url", "agency_timezone"}, agencyRows); err != nil {
		return nil, err
	}

	routeIDs := make([]string, 0, len(net.Routes))
	for id := range net.Routes {
		routeIDs = append(routeIDs, id)
	}
	sort.Strings(routeIDs)
	var routeRows [][]string
	for _, id := range routeIDs {
		r := net.Routes[id]
		agencyID := "0"
		for _, c := range carriers {
			if r.ProviderID != "" && c.ID != "" {
				agencyID = carriers[0].ID
				break
			}
		}
		routeRows = append(routeRows, []string{r.ID, agencyID, r.ShortName, r.LongName, gtfsRouteType(r.Mode)})
	}
	if err := writeCSV(zw, "routes.txt", []string{"route_id", "agency_id", "route_short_name", "route_long_name", "route_type"}, routeRows); err != nil {
		return nil, err
	}

	stopIDs := make([]string, 0, len(net.Stops))
	for id := range net.Stops {
		stopIDs = append(stopIDs, id)
	}
	sort.Strings(stopIDs)
	var stopRows [][]string
	for _, id := range stopIDs {
		s := net.Stops[id]
		zone := ""
		if z, ok := net.StopZones[id]; ok {
			zone = z
		}
		stopRows = append(stopRows, []string{s.ID, s.Name, fmt.Sprintf("%.6f", s.Lat), fmt.Sprintf("%.6f", s.Lon), zone})
	}
	if err := writeCSV(zw, "stops.txt", []string{"stop_id", "stop_name", "stop_lat", "stop_lon", "zone_id"}, stopRows); err != nil {
		return nil, err
	}

	tripIDs := make([]string, 0, len(net.Trips))
	for id := range net.Trips {
		tripIDs = append(tripIDs, id)
	}
	sort.Strings(tripIDs)
	var tripRows [][]string
	for _, id := range tripIDs {
		t := net.Trips[id]
		serviceID := fmt.Sprintf("%d", t.ServiceID)
		if serviceID == "0" {
			serviceID = "1"
		}
		tripRows = append(tripRows, []string{t.ID, t.RouteID, serviceID})
	}
	if err := writeCSV(zw, "trips.txt", []string{"trip_id", "route_id", "service_id"}, tripRows); err != nil {
		return nil, err
	}

	var stRows [][]string
	for _, tid := range tripIDs {
		t := net.Trips[tid]
		for _, st := range t.StopTimes {
			arr := fmt.Sprintf("%02d:%02d:%02d", st.ArrivalSec/3600, (st.ArrivalSec%3600)/60, st.ArrivalSec%60)
			dep := fmt.Sprintf("%02d:%02d:%02d", st.DepartureSec/3600, (st.DepartureSec%3600)/60, st.DepartureSec%60)
			stRows = append(stRows, []string{t.ID, arr, dep, st.StopID, fmt.Sprintf("%d", st.Sequence)})
		}
	}
	sort.Slice(stRows, func(i, j int) bool {
		if stRows[i][0] == stRows[j][0] {
			return stRows[i][4] < stRows[j][4]
		}
		return stRows[i][0] < stRows[j][0]
	})
	if err := writeCSV(zw, "stop_times.txt", []string{"trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence"}, stRows); err != nil {
		return nil, err
	}

	var calRows [][]string
	if len(net.Services) > 0 {
		for sid, svc := range net.Services {
			start := svc.StartDate.Format("20060102")
			end := svc.EndDate.Format("20060102")
			days := map[int]bool{}
			for _, d := range net.ServiceDays[sid] {
				days[d.Weekday] = true
			}
			mon := boolToInt(days[1])
			tue := boolToInt(days[2])
			wed := boolToInt(days[3])
			thu := boolToInt(days[4])
			fri := boolToInt(days[5])
			sat := boolToInt(days[6])
			sun := boolToInt(days[0])
			calRows = append(calRows, []string{fmt.Sprintf("%d", sid), mon, tue, wed, thu, fri, sat, sun, start, end})
		}
		sort.Slice(calRows, func(i, j int) bool { return calRows[i][0] < calRows[j][0] })
	} else {
		today := time.Now().Format("20060102")
		nextYear := time.Now().AddDate(1, 0, 0).Format("20060102")
		calRows = append(calRows, []string{"1", "1", "1", "1", "1", "1", "1", "1", today, nextYear})
	}
	if err := writeCSV(zw, "calendar.txt", []string{"service_id", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday", "start_date", "end_date"}, calRows); err != nil {
		return nil, err
	}

	var calDateRows [][]string
	for sid, exs := range net.ServiceExceptions {
		for _, ex := range exs {
			typ := "1"
			if ex.ExceptionType == model.ExceptionRemoved {
				typ = "2"
			}
			calDateRows = append(calDateRows, []string{fmt.Sprintf("%d", sid), ex.Date.Format("20060102"), typ})
		}
	}
	sort.Slice(calDateRows, func(i, j int) bool { return calDateRows[i][0] < calDateRows[j][0] })
	if err := writeCSV(zw, "calendar_dates.txt", []string{"service_id", "date", "exception_type"}, calDateRows); err != nil {
		return nil, err
	}

	var trRows [][]string
	for _, tr := range net.Transfers {
		trRows = append(trRows, []string{tr.FromStopID, tr.ToStopID, "2", fmt.Sprintf("%d", tr.Minutes*60)})
	}
	sort.Slice(trRows, func(i, j int) bool {
		if trRows[i][0] == trRows[j][0] {
			return trRows[i][1] < trRows[j][1]
		}
		return trRows[i][0] < trRows[j][0]
	})
	if err := writeCSV(zw, "transfers.txt", []string{"from_stop_id", "to_stop_id", "transfer_type", "min_transfer_time"}, trRows); err != nil {
		return nil, err
	}

	faIDs := make([]string, 0, len(net.FareAttributes))
	for id := range net.FareAttributes {
		faIDs = append(faIDs, id)
	}
	sort.Strings(faIDs)
	var faRows [][]string
	for _, id := range faIDs {
		fa := net.FareAttributes[id]
		price := fmt.Sprintf("%.2f", fa.Price)
		price = strings.TrimRight(strings.TrimRight(price, "0"), ".")
		if price == "" {
			price = "0"
		}
		currency := pricing.CurrencyOrDefault(fa.Currency)
		faRows = append(faRows, []string{fa.FareID, price, currency, "0", "", ""})
	}
	if err := writeCSV(zw, "fare_attributes.txt", []string{"fare_id", "price", "currency_type", "payment_method", "transfers", "transfer_duration"}, faRows); err != nil {
		return nil, err
	}

	var frRows [][]string
	for _, r := range net.FareRules {
		origin := ""
		if r.OriginZone != nil {
			origin = *r.OriginZone
		}
		dest := ""
		if r.DestinationZone != nil {
			dest = *r.DestinationZone
		}
		frRows = append(frRows, []string{r.FareID, r.RouteID, origin, dest, ""})
	}
	sort.Slice(frRows, func(i, j int) bool {
		if frRows[i][0] == frRows[j][0] {
			return frRows[i][1] < frRows[j][1]
		}
		return frRows[i][0] < frRows[j][0]
	})
	if err := writeCSV(zw, "fare_rules.txt", []string{"fare_id", "route_id", "origin_id", "destination_id", "contains_id"}, frRows); err != nil {
		return nil, err
	}

	h := sha256.Sum256(buf.Bytes())
	ver := time.Now().UTC().Format("20060102T150405Z") + "_" + hex.EncodeToString(h[:4])
	feedStart := ""
	feedEnd := ""
	if len(net.Services) > 0 {
		var minD, maxD time.Time
		first := true
		for _, s := range net.Services {
			if first {
				minD, maxD = s.StartDate, s.EndDate
				first = false
			} else {
				if s.StartDate.Before(minD) {
					minD = s.StartDate
				}
				if s.EndDate.After(maxD) {
					maxD = s.EndDate
				}
			}
		}
		feedStart = minD.Format("20060102")
		feedEnd = maxD.Format("20060102")
	}
	feedInfoRows := [][]string{{"travelmcp", "https://travelmcp.local", "ru", ver, feedStart, feedEnd}}
	if err := writeCSV(zw, "feed_info.txt", []string{"feed_publisher_name", "feed_publisher_url", "feed_lang", "feed_version", "feed_start_date", "feed_end_date"}, feedInfoRows); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		slog.Error("gtfs compile: zip close failed", "err", err)
		return nil, err
	}
	slog.Info("gtfs compile: done", "size", buf.Len(), "version", ver)
	return buf.Bytes(), nil
}

func CompileTo(w io.Writer, net *model.Network) error {
	data, err := Compile(net)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func boolToInt(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

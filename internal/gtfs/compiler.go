package gtfs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"travelmcp/internal/store"
)

type Compiler struct {
	feedIDPrefix string
}

func NewCompiler(prefix string) *Compiler {
	if prefix == "" {
		prefix = "f-ru"
	}
	return &Compiler{feedIDPrefix: prefix}
}

func (c *Compiler) FeedID(region string) string {
	return fmt.Sprintf("%s-%s-all", c.feedIDPrefix, region)
}

func (c *Compiler) BuildPerRegion(ctx context.Context, region string, snapshot time.Time) ([]byte, error) {
	return c.BuildPerRegionFromStore(ctx, nil, region, snapshot)
}

func (c *Compiler) BuildPerRegionFromStore(ctx context.Context, st store.Store, region string, snapshot time.Time) ([]byte, error) {
	feedID := c.FeedID(region)
	h := sha256.Sum256([]byte(feedID))
	feedVersion := fmt.Sprintf("%s_%s_%x", region, snapshot.UTC().Format("20060102T150405Z"), h[:4])
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	if st != nil {
		if err := c.writeFromStore(ctx, zw, st, region, feedID, feedVersion, snapshot); err != nil {
			zw.Close()
			return nil, err
		}
	} else {
		files := map[string]string{
			"agency.txt":          "agency_id,agency_name,agency_url,agency_timezone\n",
			"stops.txt":           "stop_id,stop_code,stop_name,stop_lat,stop_lon,zone_id\n",
			"routes.txt":          "route_id,agency_id,route_short_name,route_long_name,route_type\n",
			"trips.txt":           "trip_id,route_id,service_id\n",
			"stop_times.txt":      "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n",
			"calendar.txt":        "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n",
			"transfers.txt":       "from_stop_id,to_stop_id,transfer_type,min_transfer_time\n",
			"feed_info.txt":       fmt.Sprintf("feed_publisher_name,feed_publisher_url,feed_lang,feed_id,feed_version,feed_start_date,feed_end_date\ntravelmcp,https://travelmcp.local,ru,%s,%s,\n", feedID, feedVersion),
			"fare_attributes.txt": "fare_id,price,currency_type,payment_method,transfers\n",
			"fare_rules.txt":      "fare_id,route_id,origin_id,destination_id\n",
		}
		keys := make([]string, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, name := range keys {
			w, _ := zw.Create(name)
			_, _ = w.Write([]byte(files[name]))
		}
	}
	zw.Close()
	return buf.Bytes(), nil
}

func (c *Compiler) writeFromStore(ctx context.Context, zw *zip.Writer, st store.Store, region, feedID, feedVersion string, snapshot time.Time) error {
	// Hard-gate полноты provenance (план §3.3/§6): zip не собирается, пока
	// у живых сущностей канона есть записи без пары source/channel.
	if checker, ok := st.(store.ProvenanceCompletenessChecker); ok {
		missing, err := checker.CheckProvenanceCompleteness(ctx)
		if err != nil {
			return fmt.Errorf("gtfs provenance gate: %w", err)
		}
		if len(missing) > 0 {
			return fmt.Errorf("gtfs provenance gate: %d сущностей без пары source/channel (первые 10: %v) — zip не собирается, план §3.3", len(missing), firstN(missing, 10))
		}
	}
	net, err := st.LoadNetwork(ctx, []string{"mintrans", "gtfs"}, snapshot)
	if err != nil {
		return err
	}
	files := make(map[string]*bytes.Buffer)
	for _, name := range []string{"agency.txt", "stops.txt", "routes.txt", "trips.txt", "stop_times.txt", "calendar.txt", "calendar_dates.txt", "transfers.txt", "feed_info.txt", "fare_attributes.txt", "fare_rules.txt"} {
		files[name] = &bytes.Buffer{}
	}
	files["agency.txt"].WriteString("agency_id,agency_name,agency_url,agency_timezone,agency_lang\n")
	for id, ca := range net.Carriers {
		if ca == nil {
			continue
		}
		fmt.Fprintf(files["agency.txt"], "%s,%s,https://travelmcp.local,Europe/Moscow,ru\n", id, ca.Name)
	}
	if files["agency.txt"].Len() == len("agency_id,agency_name,agency_url,agency_timezone,agency_lang\n") {
		files["agency.txt"].WriteString("0,Неизвестный перевозчик,https://travelmcp.local,Europe/Moscow,ru\n")
	}
	files["stops.txt"].WriteString("stop_id,stop_code,stop_name,stop_lat,stop_lon,zone_id\n")
	stopIDs := make([]string, 0, len(net.Stops))
	for id := range net.Stops {
		stopIDs = append(stopIDs, id)
	}
	sort.Strings(stopIDs)
	for _, sid := range stopIDs {
		s := net.Stops[sid]
		zone := net.StopZones[sid]
		fmt.Fprintf(files["stops.txt"], "%s,%s,%s,%f,%f,%s\n", s.ID, s.ID, s.Name, s.Lat, s.Lon, zone)
	}
	_ = region
	files["routes.txt"].WriteString("route_id,agency_id,route_short_name,route_long_name,route_type\n")
	routeIDs := make([]string, 0, len(net.Routes))
	for id := range net.Routes {
		routeIDs = append(routeIDs, id)
	}
	sort.Strings(routeIDs)
	for _, rid := range routeIDs {
		r := net.Routes[rid]
		agency := "0"
		if len(net.Carriers) > 0 {
			for cid := range net.Carriers {
				agency = cid
				break
			}
		}
		_ = r.ProviderID
		routeType := "3"
		if r.Mode == "rail" {
			routeType = "2"
		}
		if r.Mode == "subway" {
			routeType = "1"
		}
		if r.Mode == "tram" {
			routeType = "0"
		}
		fmt.Fprintf(files["routes.txt"], "%s,%s,%s,%s,%s\n", r.ID, agency, r.ShortName, r.LongName, routeType)
	}
	files["trips.txt"].WriteString("trip_id,route_id,service_id,trip_headsign\n")
	tripIDs := make([]string, 0, len(net.Trips))
	for id := range net.Trips {
		tripIDs = append(tripIDs, id)
	}
	sort.Strings(tripIDs)
	for _, tid := range tripIDs {
		t := net.Trips[tid]
		fmt.Fprintf(files["trips.txt"], "%s,%s,%d,\n", t.ID, t.RouteID, t.ServiceID)
	}
	files["stop_times.txt"].WriteString("trip_id,arrival_time,departure_time,stop_id,stop_sequence,pickup_type,drop_off_type\n")
	for _, tid := range tripIDs {
		t := net.Trips[tid]
		for _, st := range t.StopTimes {
			arr := fmt.Sprintf("%02d:%02d:%02d", st.ArrivalSec/3600, (st.ArrivalSec%3600)/60, st.ArrivalSec%60)
			dep := fmt.Sprintf("%02d:%02d:%02d", st.DepartureSec/3600, (st.DepartureSec%3600)/60, st.DepartureSec%60)
			fmt.Fprintf(files["stop_times.txt"], "%s,%s,%s,%s,%d,%d,%d\n", t.ID, arr, dep, st.StopID, st.Sequence, st.PickupType, st.DropOffType)
		}
	}
	files["calendar.txt"].WriteString("service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n")
	for sid, svc := range net.Services {
		days := map[int]bool{}
		for _, d := range net.ServiceDays[sid] {
			days[d.Weekday] = true
		}
		fmt.Fprintf(files["calendar.txt"], "%d,%d,%d,%d,%d,%d,%d,%d,%s,%s\n",
			sid,
			boolToInt(days[1]), boolToInt(days[2]), boolToInt(days[3]), boolToInt(days[4]), boolToInt(days[5]), boolToInt(days[6]), boolToInt(days[0]),
			svc.StartDate.Format("20060102"), svc.EndDate.Format("20060102"))
	}
	files["calendar_dates.txt"].WriteString("service_id,date,exception_type\n")
	for sid, exs := range net.ServiceExceptions {
		for _, ex := range exs {
			typ := "1"
			if ex.ExceptionType == "removed" {
				typ = "2"
			}
			fmt.Fprintf(files["calendar_dates.txt"], "%d,%s,%s\n", sid, ex.Date.Format("20060102"), typ)
		}
	}
	files["transfers.txt"].WriteString("from_stop_id,to_stop_id,transfer_type,min_transfer_time\n")
	for _, tr := range net.Transfers {
		fmt.Fprintf(files["transfers.txt"], "%s,%s,2,%d\n", tr.FromStopID, tr.ToStopID, tr.Minutes*60)
	}
	files["feed_info.txt"].WriteString("feed_publisher_name,feed_publisher_url,feed_lang,feed_id,feed_version,feed_start_date,feed_end_date\n")
	fmt.Fprintf(files["feed_info.txt"], "travelmcp,https://travelmcp.local,ru,%s,%s,%s,%s\n", feedID, feedVersion, snapshot.Format("20060102"), snapshot.AddDate(1, 0, 0).Format("20060102"))
	files["fare_attributes.txt"].WriteString("fare_id,price,currency_type,payment_method,transfers,transfer_duration\n")
	for fid, fa := range net.FareAttributes {
		fmt.Fprintf(files["fare_attributes.txt"], "%s,%f,%s,0,,\n", fid, fa.Price, fa.Currency)
	}
	files["fare_rules.txt"].WriteString("fare_id,route_id,origin_id,destination_id,contains_id\n")
	for _, fr := range net.FareRules {
		o, d := "", ""
		if fr.OriginZone != nil {
			o = *fr.OriginZone
		}
		if fr.DestinationZone != nil {
			d = *fr.DestinationZone
		}
		fmt.Fprintf(files["fare_rules.txt"], "%s,%s,%s,%s,\n", fr.FareID, fr.RouteID, o, d)
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		w, _ := zw.Create(name)
		_, _ = w.Write(files[name].Bytes())
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// firstN — первые n элементов (для diagnostic-вывода gate).
func firstN[T any](in []T, n int) []T {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

func ArchiveName(region string) string {
	return fmt.Sprintf("gtfs_%s_all.zip", region)
}

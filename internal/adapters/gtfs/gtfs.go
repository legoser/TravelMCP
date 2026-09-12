package gtfs

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/support/classifier"
)

const ProviderID = "gtfs"

type Adapter struct {
	path string
}

func New(path string) *Adapter { return &Adapter{path: path} }

func (a *Adapter) Empty() bool { return a.path == "" }

func (a *Adapter) Load() (*model.Network, error) {
	if a.path == "" {
		return nil, fmt.Errorf("gtfs path empty")
	}
	f, err := os.Open(a.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, err
	}
	net := model.NewNetwork()
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	if err := loadStops(files, net); err != nil {
		return nil, err
	}
	if err := loadRoutes(files, net); err != nil {
		return nil, err
	}
	if err := loadTrips(files, net); err != nil {
		return nil, err
	}
	if err := loadStopTimes(files, net); err != nil {
		return nil, err
	}
	buildConnections(net)
	net.BuildIndexes()
	return net, nil
}

func openCSV(files map[string]*zip.File, name string) (*csv.Reader, io.Closer, error) {
	zf, ok := files[name]
	if !ok {
		return nil, nil, fmt.Errorf("gtfs missing %s", name)
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, nil, err
	}
	return csv.NewReader(rc), rc, nil
}

func loadStops(files map[string]*zip.File, net *model.Network) error {
	r, closer, err := openCSV(files, "stops.txt")
	if err != nil {
		return nil
	}
	defer closer.Close()
	rows, err := r.ReadAll()
	if err != nil {
		return err
	}
	if len(rows) < 1 {
		return nil
	}
	idx := headerIndex(rows[0])
	for _, row := range rows[1:] {
		id := col(row, idx, "stop_id")
		name := col(row, idx, "stop_name")
		lat := parseFloat(col(row, idx, "stop_lat"))
		lon := parseFloat(col(row, idx, "stop_lon"))
		net.Stops[id] = &model.Stop{ID: id, ProviderID: ProviderID, Name: name, Lat: lat, Lon: lon, Type: classifier.Default.Classify(name, nil)}
	}
	return nil
}

func loadRoutes(files map[string]*zip.File, net *model.Network) error {
	r, closer, err := openCSV(files, "routes.txt")
	if err != nil {
		return nil
	}
	defer closer.Close()
	rows, _ := r.ReadAll()
	if len(rows) < 1 {
		return nil
	}
	idx := headerIndex(rows[0])
	for _, row := range rows[1:] {
		id := col(row, idx, "route_id")
		short := col(row, idx, "route_short_name")
		long := col(row, idx, "route_long_name")
		typ := col(row, idx, "route_type")
		mode := gtfsMode(typ)
		net.Routes[id] = &model.Route{ID: id, ProviderID: ProviderID, ShortName: short, LongName: long, Mode: mode}
	}
	return nil
}

func loadTrips(files map[string]*zip.File, net *model.Network) error {
	r, closer, err := openCSV(files, "trips.txt")
	if err != nil {
		return nil
	}
	defer closer.Close()
	rows, _ := r.ReadAll()
	if len(rows) < 1 {
		return nil
	}
	idx := headerIndex(rows[0])
	for _, row := range rows[1:] {
		tid := col(row, idx, "trip_id")
		rid := col(row, idx, "route_id")
		route, ok := net.Routes[rid]
		mode := model.ModeBus
		if ok {
			mode = route.Mode
		}
		net.Trips[tid] = &model.Trip{ID: tid, RouteID: rid, ProviderID: ProviderID, Mode: mode, StopTimes: []model.StopTime{}}
	}
	return nil
}

func loadStopTimes(files map[string]*zip.File, net *model.Network) error {
	r, closer, err := openCSV(files, "stop_times.txt")
	if err != nil {
		return nil
	}
	defer closer.Close()
	rows, _ := r.ReadAll()
	if len(rows) < 1 {
		return nil
	}
	idx := headerIndex(rows[0])
	group := map[string][]model.StopTime{}
	for _, row := range rows[1:] {
		tid := col(row, idx, "trip_id")
		sid := col(row, idx, "stop_id")
		seq := parseInt(col(row, idx, "stop_sequence"))
		arr := parseGTFSSeconds(col(row, idx, "arrival_time"))
		dep := parseGTFSSeconds(col(row, idx, "departure_time"))
		group[tid] = append(group[tid], model.StopTime{StopID: sid, Sequence: seq, ArrivalSec: arr, DepartureSec: dep})
	}
	for tid, times := range group {
		if trip, ok := net.Trips[tid]; ok {
			sort.Slice(times, func(i, j int) bool { return times[i].Sequence < times[j].Sequence })
			trip.StopTimes = times
		}
	}
	return nil
}

func buildConnections(net *model.Network) {
	dayBase := time.Now().UTC().Truncate(24 * time.Hour)
	for _, trip := range net.Trips {
		for i := 0; i < len(trip.StopTimes)-1; i++ {
			from := trip.StopTimes[i]
			to := trip.StopTimes[i+1]
			dep := dayBase.Add(time.Duration(from.DepartureSec) * time.Second)
			arr := dayBase.Add(time.Duration(to.ArrivalSec) * time.Second)
			if arr.Before(dep) {
				arr = arr.Add(24 * time.Hour)
			}
			net.Connections = append(net.Connections, model.Connection{TripID: trip.ID, ProviderID: ProviderID, RouteID: trip.RouteID, Mode: trip.Mode, From: from.StopID, To: to.StopID, Departure: dep, Arrival: arr})
		}
	}
	sort.Slice(net.Connections, func(i, j int) bool { return net.Connections[i].Departure.Before(net.Connections[j].Departure) })
}

func headerIndex(header []string) map[string]int {
	m := map[string]int{}
	for i, h := range header {
		m[h] = i
	}
	return m
}

func col(row []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i < len(row) {
		return row[i]
	}
	return ""
}

func parseFloat(s string) float64 {
	var f float64
	fmt.Sscan(s, &f)
	return f
}

func parseInt(s string) int {
	var n int
	fmt.Sscan(s, &n)
	return n
}

func parseGTFSSeconds(s string) int {
	var h, m, sec int
	fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec)
	return h*3600 + m*60 + sec
}

func gtfsMode(t string) model.Mode {
	switch t {
	case "0":
		return model.ModeTram
	case "1":
		return model.ModeRail
	case "2":
		return model.ModeRail
	case "3":
		return model.ModeBus
	case "4":
		return model.ModeFlight
	default:
		return model.ModeBus
	}
}

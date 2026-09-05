package gtfs

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"testing"
	"time"
	"travelmcp/internal/model"
)

func readCSVFromZip(data []byte, name string) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			r := csv.NewReader(rc)
			return r.ReadAll()
		}
	}
	return nil, io.ErrUnexpectedEOF
}

func TestCompileEmptyNetwork(t *testing.T) {
	net := model.NewNetwork()
	data, err := Compile(net)
	if err != nil {
		t.Fatalf("compile empty: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty zip")
	}
	rows, err := readCSVFromZip(data, "agency.txt")
	if err != nil {
		t.Fatalf("agency read: %v", err)
	}
	if len(rows) < 1 || rows[0][0] != "agency_id" {
		t.Fatalf("agency header missing %v", rows)
	}
	// placeholder agency should exist
	if len(rows) < 2 {
		t.Fatalf("agency should have at least placeholder")
	}
	// fare files should exist even if empty
	rows, err = readCSVFromZip(data, "fare_attributes.txt")
	if err != nil {
		t.Fatalf("fare_attributes read: %v", err)
	}
	if rows[0][0] != "fare_id" {
		t.Fatalf("fare header wrong")
	}
}

func TestCompileWithFares(t *testing.T) {
	net := model.NewNetwork()
	net.Carriers["c1"] = &model.Carrier{ID: "10", Name: "Test Carrier"}
	net.Routes["r1"] = &model.Route{ID: "r1", ShortName: "1", LongName: "Route 1", Mode: model.ModeBus}
	net.Stops["s1"] = &model.Stop{ID: "s1", Name: "Stop 1", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Name: "Stop 2", Lat: 55.1, Lon: 37.1}
	net.Zones["z1"] = &model.Zone{ID: "z1", NameRu: "Zone 1"}
	net.FareAttributes["f1"] = &model.FareAttribute{FareID: "f1", Price: 100, Currency: "RUB", Basis: "fare"}
	net.FareRules = append(net.FareRules, model.FareRule{FareID: "f1", RouteID: "r1"})
	net.StopZones["s1"] = "z1"
	trip := &model.Trip{ID: "t1", RouteID: "r1", ProviderID: "test", Mode: model.ModeBus, ServiceID: 1, StopTimes: []model.StopTime{{StopID: "s1", Sequence: 0, ArrivalSec: 0, DepartureSec: 0}, {StopID: "s2", Sequence: 1, ArrivalSec: 600, DepartureSec: 600}}}
	net.Trips["t1"] = trip
	net.Services[1] = &model.Service{ID: 1, Name: "daily", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)}
	for i := 0; i < 7; i++ {
		net.ServiceDays[1] = append(net.ServiceDays[1], model.ServiceDay{ServiceID: 1, Weekday: i})
	}
	data, err := Compile(net)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	faRows, _ := readCSVFromZip(data, "fare_attributes.txt")
	if len(faRows) != 2 {
		t.Fatalf("fare_attributes rows %d want 2 (header+1)", len(faRows))
	}
	if faRows[1][0] != "f1" || faRows[1][1] != "100" {
		t.Fatalf("fare row wrong %v", faRows[1])
	}
	frRows, _ := readCSVFromZip(data, "fare_rules.txt")
	if len(frRows) != 2 {
		t.Fatalf("fare_rules rows %d", len(frRows))
	}
	if frRows[1][0] != "f1" || frRows[1][1] != "r1" {
		t.Fatalf("fare_rules wrong %v", frRows[1])
	}
	stopRows, _ := readCSVFromZip(data, "stops.txt")
	found := false
	for _, r := range stopRows[1:] {
		if r[0] == "s1" && r[4] == "z1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stop s1 should have zone z1 %v", stopRows)
	}
	feedRows, _ := readCSVFromZip(data, "feed_info.txt")
	if len(feedRows) < 2 || feedRows[1][3] == "" {
		t.Fatalf("feed_info version missing %v", feedRows)
	}
}

func TestCompileNoPanicOnInvalidPrice(t *testing.T) {
	net := model.NewNetwork()
	net.FareAttributes["bad"] = &model.FareAttribute{FareID: "bad", Price: -10, Currency: "", Basis: ""}
	_, err := Compile(net)
	if err != nil {
		t.Fatalf("compile with bad fare should not error: %v", err)
	}
}

func TestCompileTransfersEmpty(t *testing.T) {
	net := model.NewNetwork()
	net.Stops["s1"] = &model.Stop{ID: "s1", Name: "A", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Name: "B", Lat: 55.1, Lon: 37.1}
	net.Transfers = append(net.Transfers, model.Transfer{FromStopID: "s1", ToStopID: "s2", Minutes: 5})
	data, err := Compile(net)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rows, _ := readCSVFromZip(data, "transfers.txt")
	if len(rows) < 2 {
		t.Fatalf("transfers should have data")
	}
	if rows[1][0] != "s1" || rows[1][1] != "s2" {
		t.Fatalf("transfer wrong %v", rows[1])
	}
}

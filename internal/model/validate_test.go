package model

import (
	"testing"
	"time"
)

func TestValidateNetworkExcludesZeroCoords(t *testing.T) {
	net := NewNetwork()
	net.Stops["good"] = &Stop{ID: "good", ProviderID: "test", Name: "Good", Lat: 55, Lon: 83}
	net.Stops["bad"] = &Stop{ID: "bad", ProviderID: "test", Name: "Bad", Lat: 0, Lon: 0}
	net.Stops["empty"] = &Stop{ID: "empty", ProviderID: "test", Name: "", Lat: 55, Lon: 83}
	net.Connections = []Connection{{TripID: "t1", ProviderID: "test", From: "good", To: "bad", Departure: time.Now(), Arrival: time.Now().Add(-time.Hour)}}
	issues := ValidateNetwork(net)
	if len(issues) < 3 {
		t.Fatalf("want >=3 issues, got %d: %+v", len(issues), issues)
	}
	excluded := FilterExcludedStops(net, issues)
	if !excluded["bad"] {
		t.Fatalf("bad should be excluded")
	}
	if excluded["good"] {
		t.Fatalf("good should not be excluded")
	}
	hasErr := false
	for _, is := range issues {
		if is.Level == IssueError && is.Entity == "connection:0:t1" {
			hasErr = true
		}
	}
	if !hasErr {
		t.Fatalf("want error for connection arrival before departure")
	}
}

package providers

import (
	"path/filepath"
	"testing"
	"time"

	"travelmcp/internal/model"
)

func TestIntercityReestr(t *testing.T) {
	p := NewIntercity(filepath.Join("..", "..", "testdata", "reestr", "mini.json"), time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC))
	net, err := p.Network()
	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	if got, want := len(net.Routes), 4; got != want {
		t.Errorf("routes = %d, want %d", got, want)
	}
	if got, want := len(net.Trips), 8; got != want {
		t.Errorf("trips = %d, want %d", got, want)
	}
	if got := net.Routes["54.22.078"].Mode; got != model.ModeBus {
		t.Errorf("route mode = %v, want %v", got, model.ModeBus)
	}

	checks := []struct {
		trip                   string
		from, to               string
		dep, firstArr, lastArr time.Duration
	}{
		{"54.22.078-forward-0", "op:54:54098", "op:22:22165", 12*time.Hour + 30*time.Minute, 12*time.Hour + 30*time.Minute, 17 * time.Hour},
		{"54.22.078-backward-0", "op:22:22165", "op:54:54098", 19*time.Hour + 59*time.Minute, 19*time.Hour + 59*time.Minute, 24 * time.Hour},
	}
	for _, c := range checks {
		trip := net.Trips[c.trip]
		if trip == nil {
			t.Errorf("trip %s not found", c.trip)
			continue
		}
		first, last := trip.StopTimes[0], trip.StopTimes[len(trip.StopTimes)-1]
		if first.StopID != c.from || last.StopID != c.to {
			t.Errorf("%s: endpoints %s→%s, want %s→%s", c.trip, first.StopID, last.StopID, c.from, c.to)
		}
		if got := first.Departure.Sub(p.day); got != c.dep {
			t.Errorf("%s: departure offset %v, want %v", c.trip, got, c.dep)
		}
		if got := first.Arrival.Sub(p.day); got != c.firstArr {
			t.Errorf("%s: arrival of first stop %v, want %v", c.trip, got, c.firstArr)
		}
		if got := last.Arrival.Sub(p.day); got != c.lastArr {
			t.Errorf("%s: final arrival %v, want %v", c.trip, got, c.lastArr)
		}
	}

	trip := net.Trips["54.22.078-forward-0"]
	if trip == nil {
		t.Fatal("trip 54.22.078-forward-0 not found")
	}
	if got := len(trip.StopTimes); got != 7 {
		t.Errorf("stop times = %d, want 7", got)
	}
}

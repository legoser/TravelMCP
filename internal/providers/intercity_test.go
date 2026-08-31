package providers

import (
	"path/filepath"
	"testing"
	"time"

	"travelmcp/internal/model"
)

func TestIntercityReestr(t *testing.T) {
	day := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	p := NewIntercity(filepath.Join("..", "..", "testdata", "reestr", "mini.json"), day)
	net, err := p.NetworkForDay(day)
	if err != nil {
		t.Fatalf("NetworkForDay: %v", err)
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
		if got := time.Duration(first.DepartureSec) * time.Second; got != c.dep {
			t.Errorf("%s: departure offset %v, want %v", c.trip, got, c.dep)
		}
		if got := time.Duration(first.ArrivalSec) * time.Second; got != c.firstArr {
			t.Errorf("%s: arrival of first stop %v, want %v", c.trip, got, c.firstArr)
		}
		if got := time.Duration(last.ArrivalSec) * time.Second; got != c.lastArr {
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

func TestIntercityDifferentDay(t *testing.T) {
	day1 := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
	p := NewIntercity(filepath.Join("..", "..", "testdata", "reestr", "mini.json"), day1)

	net1, err := p.NetworkForDay(day1)
	if err != nil {
		t.Fatalf("NetworkForDay day1: %v", err)
	}
	net2, err := p.NetworkForDay(day2)
	if err != nil {
		t.Fatalf("NetworkForDay day2: %v", err)
	}

	// Рейсы должны быть привязаны к разным дням
	trip1 := net1.Trips["54.22.078-forward-0"]
	trip2 := net2.Trips["54.22.078-forward-0"]
	if trip1 == nil || trip2 == nil {
		t.Fatal("trips not found")
	}

	// Восстанавливаем абсолютное время из dayBase + seconds
	dep1 := day1.Add(time.Duration(trip1.StopTimes[0].DepartureSec) * time.Second)
	dep2 := day2.Add(time.Duration(trip2.StopTimes[0].DepartureSec) * time.Second)

	// Разница во времени отправления должна быть ровно 7 дней
	diff := dep2.Sub(dep1)
	if diff != 7*24*time.Hour {
		t.Errorf("departure diff = %v, want %v", diff, 7*24*time.Hour)
	}

	// Одинаковое время суток
	if dep1.Hour() != dep2.Hour() || dep1.Minute() != dep2.Minute() {
		t.Errorf("time of day mismatch: %02d:%02d vs %02d:%02d", dep1.Hour(), dep1.Minute(), dep2.Hour(), dep2.Minute())
	}
}

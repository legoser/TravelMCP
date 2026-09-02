package providers

import (
	"fmt"
	"sort"
	"time"

	"travelmcp/internal/classifier"
	"travelmcp/internal/geo"
	"travelmcp/internal/model"
)

const SynthID = "synth"

type Synth struct {
	day time.Time
}

func NewSynth(now time.Time) *Synth {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return &Synth{day: day}
}

func (s *Synth) ID() string {
	return SynthID
}

func (s *Synth) Health() HealthStatus {
	net, err := s.Network()
	status := HealthStatus{Up: err == nil, LastImportTime: time.Now()}
	if err == nil {
		status.Records = len(net.Stops) + len(net.Trips)
		issues := model.ValidateNetwork(net)
		status.Issues = len(issues)
		status.ExcludedStops = len(model.FilterExcludedStops(net, issues))
	}
	return status
}

func (s *Synth) Capabilities() Capabilities {
	return Capabilities{Modes: []model.Mode{model.ModeBus, model.ModeTram, model.ModeRail, model.ModeFlight}}
}

// NetworkForDay строит синтетическую сеть с рейсами на указанный день.
func (s *Synth) NetworkForDay(day time.Time) (*model.Network, error) {
	dayBase := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	return s.build(dayBase), nil
}

func (s *Synth) addStop(net *model.Network, id, name string, lat, lon float64) {
	net.Stops[id] = &model.Stop{ID: id, ProviderID: SynthID, Name: name, Lat: lat, Lon: lon, Type: classifier.Ru.Classify(name, nil)}
}

func (s *Synth) Network() (*model.Network, error) {
	return s.build(s.day), nil
}

func (s *Synth) build(dayBase time.Time) *model.Network {
	net := model.NewNetwork()
	net.Services[1] = &model.Service{ID: 1, Name: "ежедневно"}
	for wd := 0; wd < 7; wd++ {
		net.ServiceDays[1] = append(net.ServiceDays[1], model.ServiceDay{ServiceID: 1, Weekday: wd})
	}

	s.addStop(net, "a-cen", "Пермь, Центральная площадь", 58.0135, 56.2495)
	s.addStop(net, "a-bus", "Пермь, Автовокзал", 58.0050, 56.2470)
	s.addStop(net, "b-bus", "Екатеринбург, Автовокзал", 56.8340, 60.5970)
	s.addStop(net, "b-mkt", "Екатеринбург, Центральный рынок", 56.8400, 60.6070)
	s.addStop(net, "c1", "ПГУ, Остановка Студенческая", 58.0030, 56.2850)
	s.addStop(net, "c2", "ПГУ, Остановка Парк", 58.0060, 56.2900)
	s.addStop(net, "c2x", "ПГУ, Остановка Площадь", 58.0080, 56.2920)
	s.addStop(net, "c3", "ПГУ, Вокзал", 58.0100, 56.2950)
	s.addStop(net, "a-air", "Пермь, Поезд (вокзал)", 58.0100, 56.2300)
	s.addStop(net, "a-apt", "Пермь, аэропорт Большое Савино", 57.9148, 56.0217)
	s.addStop(net, "b-apt", "Екатеринбург, аэропорт Кольцово", 56.7431, 60.8028)

	net.Transfers = append(net.Transfers,
		model.Transfer{FromStopID: "c2", ToStopID: "c2x", Minutes: 5, MinTransferTime: 5, DistanceM: 300},
		model.Transfer{FromStopID: "c2x", ToStopID: "c2", Minutes: 5, MinTransferTime: 5, DistanceM: 300},
		model.Transfer{FromStopID: "b-bus", ToStopID: "b-mkt", Minutes: 6, MinTransferTime: 6, DistanceM: 350},
		model.Transfer{FromStopID: "b-mkt", ToStopID: "b-bus", Minutes: 6, MinTransferTime: 6, DistanceM: 350},
	)

	s.addLine(net, model.Route{ID: "a", ShortName: "1", LongName: "Центральная площадь — Автовокзал", Mode: model.ModeBus},
		[]string{"a-cen", "a-bus"}, []int{15}, 360, 10, 1370, dayBase)
	s.addLine(net, model.Route{ID: "900", ShortName: "900", LongName: "Пермь — Екатеринбург", Mode: model.ModeBus},
		[]string{"a-bus", "b-bus"}, []int{150}, 360, 60, 1380, dayBase)
	s.addLine(net, model.Route{ID: "b", ShortName: "5", LongName: "Автовокзал — Центральный рынок", Mode: model.ModeTram},
		[]string{"b-bus", "b-mkt"}, []int{12}, 360, 6, 1380, dayBase)
	s.addLine(net, model.Route{ID: "c", ShortName: "8", LongName: "Студенческая — Парк", Mode: model.ModeBus},
		[]string{"c1", "c2"}, []int{8}, 360, 8, 1380, dayBase)
	s.addLine(net, model.Route{ID: "d", ShortName: "5", LongName: "Площадь — Вокзал", Mode: model.ModeBus},
		[]string{"c2x", "c3"}, []int{8}, 360, 8, 1380, dayBase)
	s.addLine(net, model.Route{ID: "r", ShortName: "Э", LongName: "Автовокзал — Поезд", Mode: model.ModeRail},
		[]string{"a-bus", "a-air"}, []int{10}, 360, 15, 1380, dayBase)
	s.addLine(net, model.Route{ID: "s1", ShortName: "С", LongName: "Пермь, Автовокзал — Аэропорт", Mode: model.ModeBus},
		[]string{"a-bus", "a-apt"}, []int{30}, 300, 30, 1410, dayBase)
	s.addLine(net, model.Route{ID: "s2", ShortName: "С", LongName: "Екатеринбург, Аэропорт — Автовокзал", Mode: model.ModeBus},
		[]string{"b-apt", "b-bus"}, []int{30}, 300, 30, 1410, dayBase)
	s.addLine(net, model.Route{ID: "fly", ShortName: "7V", LongName: "Пермь — Екатеринбург", Mode: model.ModeFlight},
		[]string{"a-apt", "b-apt"}, []int{65}, 480, 120, 1260, dayBase)

	sort.Slice(net.Connections, func(i, j int) bool {
		return net.Connections[i].Departure.Before(net.Connections[j].Departure)
	})
	net.BuildIndexes()
	return net
}

func (s *Synth) addLine(net *model.Network, route model.Route, stops []string, travel []int, firstMin, stepMin, lastMin int, dayBase time.Time) {
	net.Routes[route.ID] = &route
	for dep := firstMin; dep <= lastMin; dep += stepMin {
		tripID := fmt.Sprintf("%s-%02d%02d", route.ID, dep/60, dep%60)
		stopTimes := make([]model.StopTime, 0, len(stops))
		t := dayBase.Add(time.Duration(dep) * time.Minute)
		for i, stop := range stops {
			stopTimes = append(stopTimes, model.StopTime{StopID: stop, Sequence: i, ArrivalSec: int(t.Sub(dayBase).Seconds()), DepartureSec: int(t.Sub(dayBase).Seconds())})
			if i < len(travel) {
				t = t.Add(time.Duration(travel[i]) * time.Minute)
			}
		}
		net.Trips[tripID] = &model.Trip{
			ID:         tripID,
			RouteID:    route.ID,
			ProviderID: SynthID,
			Mode:       route.Mode,
			ServiceID:  1, // Changed from "everyday" to 1 as placeholder
			StopTimes:  stopTimes,
		}
		for i := 0; i < len(stops)-1; i++ {
			fromStop := net.Stops[stops[i]]
			toStop := net.Stops[stops[i+1]]
			dist := 0
			if fromStop != nil && toStop != nil {
				dist = int(geo.Haversine(fromStop.Coordinates(), toStop.Coordinates()) * 1000)
			}
			net.Connections = append(net.Connections, model.Connection{
				TripID:     tripID,
				ProviderID: SynthID,
				RouteID:    route.ID,
				Mode:       route.Mode,
				From:       stops[i],
				To:         stops[i+1],
				Departure:  dayBase.Add(time.Duration(stopTimes[i].DepartureSec) * time.Second),
				Arrival:    dayBase.Add(time.Duration(stopTimes[i+1].ArrivalSec) * time.Second),
				DistanceM:  dist,
			})
		}
	}
}

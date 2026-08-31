package model

import (
	"fmt"
	"strings"
)

type IssueLevel string

const (
	IssueWarn  IssueLevel = "warn"
	IssueError IssueLevel = "error"
)

type Issue struct {
	Level      IssueLevel `json:"level"`
	ProviderID string     `json:"provider_id"`
	Entity     string     `json:"entity"`
	Message    string     `json:"message"`
}

func ValidateNetwork(net *Network) []Issue {
	var issues []Issue
	seenStop := map[string]string{}
	for id, s := range net.Stops {
		if _, dup := seenStop[id]; dup {
			issues = append(issues, Issue{Level: IssueError, ProviderID: s.ProviderID, Entity: "stop:" + id, Message: "дублирующийся StopID"})
		}
		seenStop[id] = s.ProviderID
		if s.Name == "" {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: s.ProviderID, Entity: "stop:" + id, Message: "пустое имя остановки"})
		}
		if s.Lat == 0 && s.Lon == 0 {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: s.ProviderID, Entity: "stop:" + id, Message: "координаты (0,0) — исключена"})
		} else if s.Lat < 35 || s.Lat > 85 || s.Lon < 19 || s.Lon > 190 || s.Lat < -90 || s.Lat > 90 || s.Lon < -180 || s.Lon > 180 {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: s.ProviderID, Entity: "stop:" + id, Message: fmt.Sprintf("координаты вне РФ/СНГ диапазона: %.4f,%.4f", s.Lat, s.Lon)})
		}
	}
	seenTrip := map[string]bool{}
	for id, t := range net.Trips {
		if seenTrip[id] {
			issues = append(issues, Issue{Level: IssueError, ProviderID: t.ProviderID, Entity: "trip:" + id, Message: "дублирующийся TripID"})
		}
		seenTrip[id] = true
		if len(t.StopTimes) == 0 {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: t.ProviderID, Entity: "trip:" + id, Message: "пустой StopTimes"})
		}
		for _, st := range t.StopTimes {
			if _, ok := net.Stops[st.StopID]; !ok {
				issues = append(issues, Issue{Level: IssueWarn, ProviderID: t.ProviderID, Entity: "trip:" + id, Message: fmt.Sprintf("stop %s не найден", st.StopID)})
			}
			if st.ArrivalSec < 0 || st.DepartureSec < 0 {
				issues = append(issues, Issue{Level: IssueWarn, ProviderID: t.ProviderID, Entity: "trip:" + id, Message: "отрицательные секунды StopTime"})
			}
		}
	}
	for i, c := range net.Connections {
		if c.Arrival.Before(c.Departure) {
			issues = append(issues, Issue{Level: IssueError, ProviderID: c.ProviderID, Entity: fmt.Sprintf("connection:%d:%s", i, c.TripID), Message: fmt.Sprintf("Arrival %s раньше Departure %s", c.Arrival.Format("15:04"), c.Departure.Format("15:04"))})
		}
		if _, ok := net.Stops[c.From]; !ok {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: c.ProviderID, Entity: fmt.Sprintf("connection:%d", i), Message: fmt.Sprintf("from stop %s не найден", c.From)})
		}
		if _, ok := net.Stops[c.To]; !ok {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: c.ProviderID, Entity: fmt.Sprintf("connection:%d", i), Message: fmt.Sprintf("to stop %s не найден", c.To)})
		}
		if c.TripID == "" {
			issues = append(issues, Issue{Level: IssueWarn, ProviderID: c.ProviderID, Entity: fmt.Sprintf("connection:%d", i), Message: "пустой TripID"})
		}
	}
	for i, tr := range net.Transfers {
		if _, ok := net.Stops[tr.FromStopID]; !ok {
			issues = append(issues, Issue{Level: IssueWarn, Entity: fmt.Sprintf("transfer:%d", i), Message: fmt.Sprintf("from %s не найден", tr.FromStopID)})
		}
		if _, ok := net.Stops[tr.ToStopID]; !ok {
			issues = append(issues, Issue{Level: IssueWarn, Entity: fmt.Sprintf("transfer:%d", i), Message: fmt.Sprintf("to %s не найден", tr.ToStopID)})
		}
	}
	return issues
}

func FilterExcludedStops(net *Network, issues []Issue) map[string]bool {
	excluded := map[string]bool{}
	for _, is := range issues {
		if is.Level != IssueWarn {
			continue
		}
		if len(is.Entity) > 5 && is.Entity[:5] == "stop:" {
			id := is.Entity[5:]
			if strings.Contains(is.Message, "координаты") {
				excluded[id] = true
			}
		}
	}
	return excluded
}

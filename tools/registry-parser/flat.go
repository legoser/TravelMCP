package main

import (
	"sort"
	"strconv"
	"strings"
)

// Flat-контракт (структурно = model.FlatTrip / model.FlatPayload из
// главного модуля travelmcp; дублируется, чтобы модуль оставался
// standalone — «минимум импортов и встраиваний»).

type AdaptedIdentifier struct {
	System   string `json:"system"`
	CodeType string `json:"code_type"`
	Code     string `json:"code"`
}

type FlatStop struct {
	StopID string              `json:"stop_id"`
	Name   string              `json:"name"`
	Region string              `json:"region"`
	Codes  []AdaptedIdentifier `json:"codes,omitempty"`
	Lat    *float64            `json:"lat,omitempty"`
	Lon    *float64            `json:"lon,omitempty"`
	ArrMin *int                `json:"arr_min,omitempty"`
	DepMin *int                `json:"dep_min,omitempty"`
	// IsFuzzy — время ориентировочное (в источнике нет, интерполируется
	// при промоушене): «время уточнять у перевозчика».
	IsFuzzy bool `json:"is_fuzzy,omitempty"`
}

type FlatTrip struct {
	RouteNK       string     `json:"route_nk,omitempty"`
	RouteReg      string     `json:"route_reg"`
	Direction     string     `json:"direction"`
	ServiceID     int64      `json:"service_id"`
	Run           int        `json:"run"`
	Period        string     `json:"period"`
	Carrier       string     `json:"carrier"`
	CarrierINN    string     `json:"carrier_inn"`
	RouteFrom     string     `json:"route_from"`
	RouteTo       string     `json:"route_to"`
	Stops         []FlatStop `json:"stops"`
	Untimed       []string   `json:"untimed,omitempty"`
	FrequencyOnly bool       `json:"frequency_only"`
	Weekdays      []int      `json:"weekdays,omitempty"`
}

type FlattenStats struct {
	Schedules            int `json:"schedules"`
	Runs                 int `json:"runs"`
	Trips                int `json:"trips"`
	FrequencyOnly        int `json:"frequency_only"`
	DroppedEmptyWeekdays int `json:"dropped_empty_weekdays"`
	RestrictedTrips      int `json:"restricted_trips"`
	ParityTrips          int `json:"parity_trips"`
}

type FlatMeta struct {
	Source    string `json:"source"`
	Snapshot  string `json:"snapshot,omitempty"`
	Generator string `json:"generator,omitempty"`
}

type FlatPayload struct {
	Meta  FlatMeta     `json:"meta"`
	Stats FlattenStats `json:"stats"`
	Trips []FlatTrip   `json:"trips"`
}

// FormatWeekdays — канонический формат дней недели (вс=0..сб=6):
// отсортированный без дублей список "0,1,5"; пустой список = вся неделя.
func FormatWeekdays(days []int) string {
	seen := make(map[int]struct{}, len(days))
	out := make([]int, 0, len(days))
	for _, d := range days {
		if d < 0 || d > 6 {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	if len(out) == 0 {
		return ""
	}
	sort.Ints(out)
	parts := make([]string, len(out))
	for i, d := range out {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ",")
}

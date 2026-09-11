package yandex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/support/httpx"
)

func TestParseRaspDays(t *testing.T) {
	cases := []struct {
		in   string
		want []int
		ok   bool
	}{
		{"ежедневно", []int{0, 1, 2, 3, 4, 5, 6}, true},
		{"", []int{0, 1, 2, 3, 4, 5, 6}, true},
		{"ежедневно, кроме сб, вс", []int{1, 2, 3, 4, 5}, true},
		{"ежедневно, кроме вс", []int{1, 2, 3, 4, 5, 6}, true},
		{"пн, ср, пт", []int{1, 3, 5}, true},
		{"пт", []int{5}, true},
		{"4, 6, 8, 10 сентября", nil, false},
		{"по чётным", nil, false},
	}
	for _, c := range cases {
		got, ok := ParseRaspDays(c.in)
		if ok != c.ok {
			t.Fatalf("ParseRaspDays(%q): ok=%v want %v", c.in, ok, c.ok)
		}
		if !ok {
			continue
		}
		if len(got) != len(c.want) {
			t.Fatalf("ParseRaspDays(%q) = %v want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("ParseRaspDays(%q) = %v want %v", c.in, got, c.want)
			}
		}
	}
}

func TestParseRaspClock(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"2026-09-04 00:15:00", 15, true},
		{"2026-09-04T00:15:00+07:00", 15, true},
		{"23:59:00", 23*60 + 59, true},
		{"2026-09-04 12:05", 12*60 + 5, true},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseRaspClock(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("parseRaspClock(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFlattenRaspThread(t *testing.T) {
	dep := "2026-09-04 00:15:00"
	arr := "2026-09-04 03:30:00"
	dep2 := "2026-09-04 03:35:00"
	thread := &RaspThread{
		UID:           "empty_0_f9623379t9776009_168",
		Title:         "Кемерово — Аэропорт Толмачёво",
		TransportType: "bus",
		Carrier: struct {
			Code  int    `json:"code"`
			Title string `json:"title"`
		}{Code: 67064, Title: "ИП Мурадханян А.В."},
		Days: "ежедневно",
		Stops: []RaspThreadStop{
			{Station: stationOf("s9623379", "Кемерово, автовокзал"), Departure: &dep},
			{Station: stationOf("s9657036", "Топки", "bus_stop"), Arrival: &arr, Departure: &dep2},
		},
	}
	res := FlattenRaspThread(thread, RaspFlattenConfig{Region: "Кемеровская область - Кузбасс"})
	if res.State != "promoted" || res.Trip == nil {
		t.Fatalf("state=%s detail=%s", res.State, res.Detail)
	}
	ft := res.Trip
	if len(ft.Stops) != 2 {
		t.Fatalf("stops=%d", len(ft.Stops))
	}
	if ft.Stops[0].DepMin == nil || *ft.Stops[0].DepMin != 15 {
		t.Fatalf("первый стоп dep_min=15 (00:15 от полуночи), got %v", ft.Stops[0].DepMin)
	}
	if ft.Stops[1].ArrMin == nil || *ft.Stops[1].ArrMin != 3*60+30 {
		t.Fatalf("второй стоп arr_min=210 (03:30 от полуночи), got %v", ft.Stops[1].ArrMin)
	}
	if ft.Weekdays[0] != 0 || len(ft.Weekdays) != 7 {
		t.Fatalf("weekdays=%v", ft.Weekdays)
	}
	if ft.Stops[0].Codes[0].Code != "s9623379" || ft.Stops[0].Codes[0].System != "yandex" {
		t.Fatalf("codes=%+v", ft.Stops[0].Codes)
	}
}

func TestFlattenRaspThreadMidnight(t *testing.T) {
	dep := "2026-09-04 23:50:00"
	arr := "2026-09-05 00:20:00"
	thread := &RaspThread{
		UID:   "u1",
		Title: "Ночной",
		Days:  "ежедневно",
		Stops: []RaspThreadStop{
			{Station: stationOf("s1", "A"), Departure: &dep},
			{Station: stationOf("s2", "B"), Arrival: &arr},
		},
	}
	res := FlattenRaspThread(thread, RaspFlattenConfig{})
	if res.State != "promoted" {
		t.Fatalf("state=%s", res.State)
	}
	if got := res.Trip.Stops[1].ArrMin; got == nil || *got != 24*60+20 {
		t.Fatalf("переход через полночь: 00:20 после 23:50 → 24*60+20 мин, got %v", got)
	}
}

func TestFlattenRaspThreadRestricted(t *testing.T) {
	dep := "2026-09-04 10:00:00"
	thread := &RaspThread{
		UID:   "u2",
		Title: "Сезонный",
		Days:  "4, 6, 8, 10 сентября",
		Stops: []RaspThreadStop{
			{Station: stationOf("s1", "A"), Departure: &dep},
			{Station: stationOf("s2", "B"), Departure: &dep},
		},
	}
	res := FlattenRaspThread(thread, RaspFlattenConfig{})
	if res.State != "restricted_days" {
		t.Fatalf("state=%s want restricted_days", res.State)
	}
}

func TestFlattenRaspThreadOneStop(t *testing.T) {
	dep := "2026-09-04 10:00:00"
	thread := &RaspThread{UID: "u3", Title: "X", Days: "ежедневно", Stops: []RaspThreadStop{
		{Station: stationOf("s1", "A"), Departure: &dep},
	}}
	if res := FlattenRaspThread(thread, RaspFlattenConfig{}); res.State != "empty" {
		t.Fatalf("state=%s want empty", res.State)
	}
}

func stationOf(code, title string, extra ...string) (s struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	StationType string `json:"station_type"`
	Transport   string `json:"transport_type"`
}) {
	s.Code = code
	s.Title = title
	if len(extra) > 0 {
		s.StationType = extra[0]
	}
	if len(extra) > 1 {
		s.Transport = extra[1]
	}
	return s
}

func TestRaspOfflineScheduleFromCache(t *testing.T) {
	dir := t.TempDir()
	item := RaspScheduleItem{}
	item.Thread.UID = "empty_0_f9623379t9776009_168"
	station := &RaspSchedule{
		Pagination: struct {
			Total  int `json:"total"`
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}{Total: 1, Limit: 100, Offset: 0},
		Schedule: []RaspScheduleItem{item},
	}
	raw, _ := json.Marshal(station)
	if err := os.WriteFile(filepath.Join(dir, "schedule_s9623379_2026-09-04.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRasp(config.Config{}, httpx.New(nil, "rasp-test"), dir, nil, true)
	got, err := r.Schedule(context.Background(), "s9623379", "2026-09-04")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Schedule) != 1 {
		t.Fatalf("schedule items=%d", len(got.Schedule))
	}
	if r.Stats().ScheduleCache != 1 || r.Stats().ScheduleAPI != 0 {
		t.Fatalf("stats=%+v", r.Stats())
	}

	if _, err := r.Schedule(context.Background(), "s0000000", "2026-09-04"); err == nil {
		t.Fatal("offline без кэша обязан падать")
	}
}

func TestRaspThreadQuotaGate(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{}
	cfg.Yandex.RaspURL = "http://127.0.0.1:1"
	cfg.Yandex.RaspKey = "test-key"
	calls := 0
	quota := func(context.Context, string, int) (bool, int, error) { calls++; return false, 500, nil }
	r := NewRasp(cfg, httpx.New(nil, "rasp-test"), dir, quota, false)
	if _, err := r.Thread(context.Background(), "u_missing"); err == nil {
		t.Fatal("заблокированная квота обязана давать ошибку")
	}
	if r.Stats().QuotaBlocked != 1 || calls != 1 {
		t.Fatalf("quota не вызвана ровно один раз: %+v calls=%d", r.Stats(), calls)
	}
}

func TestRaspThreadCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	thread := &RaspThread{UID: "empty_0_f9623379t9776009_168", Title: "Кемерово — Аэропорт Толмачёво", Days: "ежедневно"}
	raw, _ := json.Marshal(thread)
	if err := os.WriteFile(filepath.Join(dir, "thread_empty_0_f9623379t9776009_168.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRasp(config.Config{}, httpx.New(nil, "rasp-test"), dir, nil, true)
	got, err := r.Thread(context.Background(), "empty_0_f9623379t9776009_168")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != thread.Title {
		t.Fatalf("title=%q", got.Title)
	}
	if r.Stats().ThreadCache != 1 {
		t.Fatalf("stats=%+v", r.Stats())
	}
	_ = time.Now
}

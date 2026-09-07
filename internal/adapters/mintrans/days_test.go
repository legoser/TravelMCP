package mintrans

import (
	"reflect"
	"testing"
)

func TestParseCellTime(t *testing.T) {
	cases := []struct {
		in       string
		mins     int
		days     []int
		hasDays  bool
		wantFail bool
	}{
		{"13:35", 815, nil, false, false},
		{"0:00", 0, nil, false, false},
		{"13:35 (пт,вс)", 815, []int{0, 5}, true, false},
		{"00:40 (пн)", 40, []int{1}, true, false},
		{"0:05 (пт, вс)", 5, []int{0, 5}, true, false},
		{"25:00", 0, nil, false, true},
		{"13:35 (xx)", 0, nil, false, true},
		{"абракадабра", 0, nil, false, true},
		{"", 0, nil, false, true},
	}
	for _, c := range cases {
		mins, days, hasDays, err := ParseCellTime(c.in)
		if c.wantFail {
			if err == nil {
				t.Errorf("%q: want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if mins != c.mins || hasDays != c.hasDays || !reflect.DeepEqual(days, c.days) {
			t.Errorf("%q: got %d %v %v", c.in, mins, days, hasDays)
		}
	}
}

func TestWeekdaySundayFirst(t *testing.T) {
	if weekdayNum["вс"] != 0 || weekdayNum["пн"] != 1 || weekdayNum["сб"] != 6 {
		t.Fatalf("конвенция вс=0..сб=6 нарушена: %v", weekdayNum)
	}
}

func TestParseBlockDays(t *testing.T) {
	all, err := ParseBlockDays("ежедневно")
	if err != nil || !all.All {
		t.Fatalf("ежедневно: %+v %v", all, err)
	}
	if _, err := ParseBlockDays(""); err != nil {
		t.Fatalf("пусто обязано означать все дни: %v", err)
	}
	none, err := ParseBlockDays("нет отправлений")
	if err != nil || !none.None {
		t.Fatalf("нет отправлений: %+v %v", none, err)
	}
	par, err := ParseBlockDays("1 через 1")
	if err != nil || !par.Parity {
		t.Fatalf("1 через 1: %+v %v", par, err)
	}
	par, err = ParseBlockDays("Через день")
	if err != nil || !par.Parity {
		t.Fatalf("Через день: %+v %v", par, err)
	}
	lst, err := ParseBlockDays("вт,чт,сб")
	if err != nil || !reflect.DeepEqual(lst.Days, []int{2, 4, 6}) {
		t.Fatalf("вт,чт,сб: %+v %v", lst, err)
	}
	union, err := ParseBlockDays("пт,сб;вс")
	if err != nil || !reflect.DeepEqual(union.Days, []int{0, 5, 6}) {
		t.Fatalf("пт,сб;вс: %+v %v", union, err)
	}
	if _, err := ParseBlockDays("иногда"); err == nil {
		t.Fatal("неизвестные дни обязаны фейлить контракт")
	}
}

func TestIsNoServiceCell(t *testing.T) {
	if !IsNoServiceCell("Нет отправления") || IsNoServiceCell("13:35") || IsNoServiceCell("") {
		t.Fatal("маркер отсутствия отправления")
	}
}

func TestNormWeekdays(t *testing.T) {
	if got := normWeekdays([]int{0, 1, 2, 3, 4, 5, 6}); got != nil {
		t.Fatalf("полная неделя обязана сворачиваться в nil: %v", got)
	}
	if got := normWeekdays([]int{5, 0, 5}); !reflect.DeepEqual(got, []int{0, 5}) {
		t.Fatalf("дедуп+сортировка: %v", got)
	}
	if FormatWeekdays([]int{0, 5}) != "0,5" || FormatWeekdays(nil) != "" {
		t.Fatal("формат service_days")
	}
}

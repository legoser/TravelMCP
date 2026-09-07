package mintrans

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var weekdayNum = map[string]int{
	"вс": 0, "пн": 1, "вт": 2, "ср": 3, "чт": 4, "пт": 5, "сб": 6,
}

var cellTimeRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?:\s*\(([^)]*)\))?$`)

func parseDayToken(tok string) (int, bool) {
	n, ok := weekdayNum[strings.ToLower(strings.TrimSpace(tok))]
	return n, ok
}

func parseDayList(s string) ([]int, bool) {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(s, ",") {
		for _, tok := range strings.Fields(part) {
			n, ok := parseDayToken(tok)
			if !ok {
				return nil, false
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	sort.Ints(out)
	return out, true
}

func IsNoServiceCell(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "нет")
}

func ParseCellTime(s string) (mins int, days []int, hasDays bool, err error) {
	m := cellTimeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, nil, false, fmt.Errorf("время %q: want HH:MM с опциональным (дни)", s)
	}
	h, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	if h > 23 || mm > 59 {
		return 0, nil, false, fmt.Errorf("время %q вне суток", s)
	}
	if strings.TrimSpace(m[3]) == "" {
		return h*60 + mm, nil, false, nil
	}
	d, ok := parseDayList(m[3])
	if !ok {
		return 0, nil, false, fmt.Errorf("время %q: неизвестные дни %q", s, m[3])
	}
	return h*60 + mm, d, true, nil
}

type BlockDays struct {
	All    bool
	None   bool
	Parity bool
	Days   []int
}

func ParseBlockDays(s string) (BlockDays, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return BlockDays{All: true}, nil
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "нет") {
		return BlockDays{None: true}, nil
	}
	if strings.Contains(low, "через") {
		return BlockDays{Parity: true}, nil
	}
	var union []int
	seen := map[int]bool{}
	for _, part := range strings.Split(low, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "ежедневно" {
			return BlockDays{All: true}, nil
		}
		d, ok := parseDayList(part)
		if !ok {
			return BlockDays{}, fmt.Errorf("дни %q: неизвестный формат", s)
		}
		for _, n := range d {
			if !seen[n] {
				seen[n] = true
				union = append(union, n)
			}
		}
	}
	if len(union) == 0 {
		return BlockDays{}, fmt.Errorf("дни %q: пустой список", s)
	}
	sort.Ints(union)
	return BlockDays{Days: union}, nil
}

func intersectDays(a, b []int) []int {
	set := map[int]bool{}
	for _, n := range a {
		set[n] = true
	}
	out := []int{}
	for _, n := range b {
		if set[n] {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

func blockDaysSet(bd BlockDays) []int {
	if len(bd.Days) > 0 {
		return append([]int{}, bd.Days...)
	}
	return allWeek()
}

func allWeek() []int { return []int{0, 1, 2, 3, 4, 5, 6} }

func normWeekdays(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	set := map[int]bool{}
	for _, n := range in {
		if n >= 0 && n <= 6 {
			set[n] = true
		}
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	if len(out) == 7 {
		return nil
	}
	return out
}

func FormatWeekdays(days []int) string {
	if len(days) == 0 {
		return ""
	}
	parts := make([]string, 0, len(days))
	for _, n := range days {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}

func parseWeekdays(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 0 && n <= 6 {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

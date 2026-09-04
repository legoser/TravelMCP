package timeutil

import (
	"regexp"
	"strconv"
	"strings"
)

var timeRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})`)

func ParseTimeMinutes(s string) (int, bool) {
	m := timeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if h < 0 || h > 23 || min < 0 || min > 59 {
		return 0, false
	}
	return h*60 + min, true
}

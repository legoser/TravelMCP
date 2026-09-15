package jobs

import (
	"errors"
	"testing"
)

func TestIsRateLimited(t *testing.T) {
	if isRateLimited(nil) {
		t.Fatal("nil must not be rate limited")
	}
	yes := []string{
		"429 quota exhausted for yandex",
		"429 Too Many Requests",
		"Rate Limit Exceeded",
		"rate_limit hit",
		"ratelimit",
		"quota exhausted",
		"please try again later",
		"too many requests, slow down",
	}
	for _, s := range yes {
		if !isRateLimited(errors.New(s)) {
			t.Errorf("%q must be rate limited", s)
		}
	}
	no := []string{
		"failed to generate report",
		"cannot operate on closed pool",
		"migrate: separate schema step failed",
		"connection refused",
		"no jobs",
		"no handler for cleanup",
		"context deadline exceeded",
	}
	for _, s := range no {
		if isRateLimited(errors.New(s)) {
			t.Errorf("%q must NOT be rate limited (was: bare 'rate' substring)", s)
		}
	}
}

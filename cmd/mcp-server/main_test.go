package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/store"
)

func TestCleanupPending(t *testing.T) {
	if cleanupPending(nil) {
		t.Fatal("empty list must not block enqueue")
	}
	done := []store.JobRow{
		{Type: "cleanup", State: "done"},
		{Type: "cleanup", State: "dead"},
		{Type: "cleanup", State: "cancelled"},
		{Type: "import_gtfs", State: "running"},
	}
	if cleanupPending(done) {
		t.Fatal("finished/foreign jobs must not block enqueue")
	}
	for _, state := range []string{"pending", "running", "retry"} {
		jobs := []store.JobRow{{Type: "import_gtfs", State: "done"}, {Type: "cleanup", State: state}}
		if !cleanupPending(jobs) {
			t.Fatalf("state %q must block duplicate enqueue", state)
		}
	}
}

func TestScheduleCleanupJobsDisabled(t *testing.T) {
	for _, raw := range []string{"off", "0s", "-1h", "not-a-duration"} {
		cfg := config.Defaults()
		cfg.Sync.CleanupInterval = raw
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			scheduleCleanupJobs(ctx, nil, cfg, slog.Default())
		}()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Fatalf("interval %q must disable scheduler (return immediately)", raw)
		}
	}
}

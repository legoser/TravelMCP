package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	store "travelmcp/internal/store"
)

type TripsAttachPayload struct {
	Source string `json:"source"`
	Route  string `json:"route"`
	Region string `json:"region,omitempty"`
	PlanID string `json:"plan_id,omitempty"`
}

func (p TripsAttachPayload) Encode() json.RawMessage {
	raw, _ := json.Marshal(p)
	return raw
}

func DecodeTripsAttachPayload(raw json.RawMessage) (TripsAttachPayload, error) {
	var p TripsAttachPayload
	if len(raw) == 0 {
		return p, fmt.Errorf("sync trips: пустой payload sync_trips_attach")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("sync trips: payload sync_trips_attach: %w", err)
	}
	if p.Route == "" {
		return p, fmt.Errorf("sync trips: payload sync_trips_attach без route")
	}
	if p.Source == "" {
		p.Source = "mintrans"
	}
	return p, nil
}

func EnqueueTripsAttachJobs(ctx context.Context, db store.Store, routes []string, base TripsAttachPayload) ([]int64, error) {
	if base.Source == "" {
		base.Source = "mintrans"
	}
	sorted := append([]string{}, routes...)
	sort.Strings(sorted)
	var ids []int64
	for _, r := range sorted {
		p := base
		p.Route = r
		id, err := db.EnqueueJob(ctx, store.JobRow{
			Type: string("sync_trips_attach"), Payload: string(p.Encode()), Region: p.Region, State: "pending",
		})
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func HandleTripsAttachJob(ctx context.Context, db store.Store, job store.JobRow, run func(ctx context.Context, route string) (AttachReport, error)) (PersistSummary, error) {
	var sum PersistSummary
	p, err := DecodeTripsAttachPayload(json.RawMessage(job.Payload))
	if err != nil {
		return sum, err
	}
	rep, err := run(ctx, p.Route)
	if err != nil {
		return sum, err
	}
	return PersistAttachReport(ctx, db, rep, p.Source)
}

func CheckAttachBarrier(skeletonDone map[string]bool, regions []string) (bool, []string) {
	var waiting []string
	for _, r := range regions {
		if !skeletonDone[r] {
			waiting = append(waiting, r)
		}
	}
	sort.Strings(waiting)
	return len(waiting) == 0, waiting
}

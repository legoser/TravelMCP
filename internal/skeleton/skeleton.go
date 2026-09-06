package skeleton

import "travelmcp/internal/model"

type EnrichmentStatus string

const (
	IdentityOnly EnrichmentStatus = "identity_only"
	Enriched     EnrichmentStatus = "enriched"
	Unverified   EnrichmentStatus = "unverified"
)

type SkeletonSource interface {
	Name() string
	Load() ([]model.AdaptedRecord, error)
}

type JoinedRecord struct {
	Record     model.AdaptedRecord
	Score      float64
	Enrichment EnrichmentStatus
}

type JoinOutcome struct {
	Canon      []JoinedRecord
	Unverified []model.AdaptedRecord
}

func extra(r model.AdaptedRecord, key string) string {
	if r.Extra == nil {
		return ""
	}
	return r.Extra[key]
}

func withExtra(r model.AdaptedRecord, key, value string) model.AdaptedRecord {
	if r.Extra == nil {
		r.Extra = map[string]string{}
	}
	r.Extra[key] = value
	return r
}

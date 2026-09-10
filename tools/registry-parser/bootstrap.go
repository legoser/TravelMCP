package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type BootstrapReport struct {
	OldSHA        string   `json:"old_sha"`
	NewSHA        string   `json:"new_sha"`
	OldRoutes     int      `json:"old_routes"`
	NewRoutes     int      `json:"new_routes"`
	Added         []string `json:"added"`
	Removed       []string `json:"removed"`
	Changed       []string `json:"changed"`
	Churn         float64  `json:"churn"`
	Disappearance float64  `json:"disappearance"`
	Threshold     float64  `json:"threshold"`
	TrustNK       bool     `json:"trust_nk"`
	Alert         bool     `json:"alert"`
}

var sanitizeRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

func CompareDatasets(oldRaw, newRaw []byte, churnThreshold float64) (BootstrapReport, error) {
	var rep BootstrapReport
	rep.Threshold = churnThreshold
	if err := ValidateDatasetContract(oldRaw); err != nil {
		return rep, fmt.Errorf("старый срез: %w", err)
	}
	if err := ValidateDatasetContract(newRaw); err != nil {
		return rep, fmt.Errorf("новый срез: %w", err)
	}
	var oldDS, newDS reestrDataset
	if err := json.Unmarshal(oldRaw, &oldDS); err != nil {
		return rep, fmt.Errorf("старый срез: %w", err)
	}
	if err := json.Unmarshal(newRaw, &newDS); err != nil {
		return rep, fmt.Errorf("новый срез: %w", err)
	}
	oh := sha256.Sum256(oldRaw)
	nh := sha256.Sum256(newRaw)
	rep.OldSHA = hex.EncodeToString(oh[:])
	rep.NewSHA = hex.EncodeToString(nh[:])
	oldSig := datasetSignatures(oldDS)
	newSig := datasetSignatures(newDS)
	rep.OldRoutes = len(oldSig)
	rep.NewRoutes = len(newSig)
	for reg := range newSig {
		if _, ok := oldSig[reg]; !ok {
			rep.Added = append(rep.Added, reg)
		}
	}
	for reg := range oldSig {
		ns, ok := newSig[reg]
		if !ok {
			rep.Removed = append(rep.Removed, reg)
			continue
		}
		if oldSig[reg] != ns {
			rep.Changed = append(rep.Changed, reg)
		}
	}
	sort.Strings(rep.Added)
	sort.Strings(rep.Removed)
	sort.Strings(rep.Changed)
	denom := rep.OldRoutes
	if rep.NewRoutes > denom {
		denom = rep.NewRoutes
	}
	if denom == 0 {
		denom = 1
	}
	rep.Churn = float64(len(rep.Added)+len(rep.Removed)+len(rep.Changed)) / float64(denom)
	if rep.OldRoutes > 0 {
		rep.Disappearance = float64(len(rep.Removed)) / float64(rep.OldRoutes)
	}
	rep.TrustNK = rep.Churn <= churnThreshold && rep.Disappearance <= churnThreshold
	rep.Alert = !rep.TrustNK
	return rep, nil
}

func datasetSignatures(ds reestrDataset) map[string]string {
	carrier := map[string]string{}
	for _, r := range ds.Routes {
		carrier[r.Reg] = r.Carrier + "|" + r.CarrierINN
	}
	stopsByRoute := map[string][]string{}
	for _, sc := range ds.Schedules {
		key := sc.Route + "|" + sc.Direction
		ids := make([]string, 0, len(sc.Stops))
		for _, s := range sc.Stops {
			ids = append(ids, s.Stop)
		}
		stopsByRoute[key] = ids
	}
	out := map[string]string{}
	for _, r := range ds.Routes {
		fw := strings.Join(stopsByRoute[r.Reg+"|forward"], ">")
		bw := strings.Join(stopsByRoute[r.Reg+"|backward"], ">")
		out[r.Reg] = carrier[r.Reg] + "#" + fw + "#" + bw
	}
	return out
}

func SyntheticRouteKey(region, number, fromStop, toStop, carrierINN string) string {
	return "synthetic:" + sanitizePart(region) + ":" + sanitizePart(number) + ":" + sanitizePart(fromStop) + ":" + sanitizePart(toStop) + ":" + sanitizePart(carrierINN)
}

func SyntheticKeyForRoute(reg, fromStop, toStop, carrierINN string) string {
	region, number := splitReg(reg)
	return SyntheticRouteKey(region, number, fromStop, toStop, carrierINN)
}

func splitReg(reg string) (string, string) {
	parts := strings.Split(reg, ".")
	if len(parts) == 0 {
		return "x", "x"
	}
	if len(parts) == 1 {
		return parts[0], "x"
	}
	return parts[0], strings.Join(parts[1:], "-")
}

func sanitizePart(s string) string {
	s = sanitizeRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	s = strings.Trim(s, "-")
	if s == "" {
		return "x"
	}
	return s
}

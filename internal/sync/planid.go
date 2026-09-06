package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

func ComputePlanID(appVersion, configHash string, rawSHAs []string) string {
	cp := append([]string{}, rawSHAs...)
	sort.Strings(cp)
	h := sha256.New()
	h.Write([]byte(appVersion))
	h.Write([]byte{0})
	h.Write([]byte(configHash))
	for _, s := range cp {
		h.Write([]byte{0})
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func ChunkFresh(planIDDone, planID string) bool {
	return planIDDone != "" && planIDDone == planID
}

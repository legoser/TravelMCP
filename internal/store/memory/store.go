package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
	"travelmcp/internal/support/classifier"
)

// Deprecated: MemoryStore для unit-тестов, не для продакшена. Используйте PostgresStore.

type CityRow = store.CityRow
type StationRow = store.StationRow
type StopRow = store.StopRow
type StationCodeRow = store.StationCodeRow
type CarrierRow = store.CarrierRow
type RouteRow = store.RouteRow
type TripRow = store.TripRow
type FrequencyRow = store.FrequencyRow
type StopTimeRow = store.StopTimeRow
type TransferRow = store.TransferRow
type QualityRow = store.QualityRow
type ServiceRow = store.ServiceRow
type ServiceDayRow = store.ServiceDayRow
type ServiceExceptionRow = store.ServiceExceptionRow
type FareRow = store.FareRow
type FareAttributeRow = store.FareAttributeRow
type FareRuleRow = store.FareRuleRow
type ZoneRow = store.ZoneRow
type StopZoneRow = store.StopZoneRow
type ImportRow = store.ImportRow
type UserRow = store.UserRow
type ApiKeyRow = store.ApiKeyRow
type PlaceRow = store.PlaceRow
type TerminalRow = store.TerminalRow

type MemoryStore struct {
	mu             sync.RWMutex
	cities         map[int64]CityRow
	stations       map[int64]StationRow
	stops          map[int64]StopRow
	stationCodes   []StationCodeRow
	carriers       map[int64]CarrierRow
	routes         map[int64]RouteRow
	trips          map[int64]TripRow
	frequencies    []FrequencyRow
	stopTimes      []StopTimeRow
	transfers      []TransferRow
	quality        []QualityRow
	imports        map[string]time.Time
	users          map[int64]UserRow
	usersByEmail   map[string]int64
	apiKeys        map[int64]ApiKeyRow
	apiKeysByKey   map[string]int64
	quotas         map[string]store.QuotaRow
	apiCalls       []store.ApiCallRow
	jobs           map[int64]store.JobRow
	auditLogs      []store.AuditLogRow
	terminals      map[int64]TerminalRow
	terminalNames  map[int64]map[string]string
	terminalTags   map[int64]map[string]string
	aliases        map[int64]map[string]store.TerminalAliasRow
	attrStates     map[string]store.AttributeStateRow
	syncRuns       map[int64]store.SyncRunRow
	syncChunks     map[int64]store.SyncChunkRow
	terminalIdents map[int64][]model.AdaptedIdentifier
	schemes        map[string]store.IdentifierSchemeRow
	provenance     map[string]store.ProvenanceVote
	reviewQueue    []store.ReviewQueueRow
	stagingTrips   map[string]store.StagingTripRow
	tripSources    map[int64]map[string]store.TripSourceRow
	outbox         []store.OutboxEvent
	nextID         int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		cities:         make(map[int64]CityRow),
		stations:       make(map[int64]StationRow),
		stops:          make(map[int64]StopRow),
		carriers:       make(map[int64]CarrierRow),
		routes:         make(map[int64]RouteRow),
		trips:          make(map[int64]TripRow),
		imports:        make(map[string]time.Time),
		users:          make(map[int64]UserRow),
		usersByEmail:   make(map[string]int64),
		apiKeys:        make(map[int64]ApiKeyRow),
		apiKeysByKey:   make(map[string]int64),
		quotas:         make(map[string]store.QuotaRow),
		jobs:           make(map[int64]store.JobRow),
		terminals:      make(map[int64]TerminalRow),
		terminalNames:  make(map[int64]map[string]string),
		terminalTags:   make(map[int64]map[string]string),
		aliases:        make(map[int64]map[string]store.TerminalAliasRow),
		attrStates:     make(map[string]store.AttributeStateRow),
		syncRuns:       make(map[int64]store.SyncRunRow),
		syncChunks:     make(map[int64]store.SyncChunkRow),
		terminalIdents: make(map[int64][]model.AdaptedIdentifier),
		schemes:        make(map[string]store.IdentifierSchemeRow),
		provenance:     make(map[string]store.ProvenanceVote),
		nextID:         1,
	}
}

func (m *MemoryStore) allocID() int64 { id := m.nextID; m.nextID++; return id }

func (m *MemoryStore) Migrate(ctx context.Context) error                            { return nil }
func (m *MemoryStore) Close() error                                                 { return nil }
func (m *MemoryStore) WithTx(ctx context.Context, fn func(store.Store) error) error { return fn(m) }
func (m *MemoryStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.stations {
		if s.Name == name && s.RegionCode == region && s.Lat != 0 {
			return s, true
		}
	}
	return StationRow{}, false
}

func (m *MemoryStore) FindStationAny(ctx context.Context, name, region string) (StationRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.stations {
		if s.Name == name && s.RegionCode == region {
			return s, true
		}
	}
	return StationRow{}, false
}
func (m *MemoryStore) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.usersByEmail[email]; ok {
		return 0, fmt.Errorf("user exists")
	}
	id := m.allocID()
	status := "pending"
	if role == "admin" {
		status = "active"
	}
	m.users[id] = UserRow{ID: id, Email: email, PassHash: passHash, Status: status, Role: role, CreatedAt: time.Now().Unix()}
	m.usersByEmail[email] = id
	return id, nil
}
func (m *MemoryStore) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usersByEmail[email]
	if !ok {
		return UserRow{}, false
	}
	u, ok := m.users[id]
	return u, ok
}
func (m *MemoryStore) GetUserByID(ctx context.Context, id int64) (UserRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	return u, ok
}
func (m *MemoryStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []UserRow
	for _, u := range m.users {
		out = append(out, u)
	}
	return out, nil
}
func (m *MemoryStore) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	u.Status = status
	m.users[id] = u
	return nil
}
func (m *MemoryStore) UpdateUserRole(ctx context.Context, id int64, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	u.Role = role
	m.users[id] = u
	return nil
}
func (m *MemoryStore) UpdateUserConfig(ctx context.Context, id int64, config string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	u.Config = config
	m.users[id] = u
	return nil
}
func (m *MemoryStore) DeleteUser(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	delete(m.users, id)
	delete(m.usersByEmail, u.Email)
	for k, v := range m.apiKeys {
		if v.UserID == id {
			delete(m.apiKeys, k)
			delete(m.apiKeysByKey, v.Key)
		}
	}
	return nil
}
func generateMemKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "tm_" + hex.EncodeToString(b)
}

func (m *MemoryStore) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scopes == "" {
		scopes = "mcp:read"
	}
	id := m.allocID()
	key := generateMemKey()
	row := ApiKeyRow{ID: id, UserID: userID, Key: key, Scopes: scopes, CreatedAt: time.Now().Unix()}
	m.apiKeys[id] = row
	m.apiKeysByKey[key] = id
	return row, nil
}
func (m *MemoryStore) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.apiKeysByKey[key]
	if !ok {
		return ApiKeyRow{}, false
	}
	r, ok := m.apiKeys[id]
	return r, ok
}
func (m *MemoryStore) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ApiKeyRow
	for _, k := range m.apiKeys {
		if k.UserID == userID {
			out = append(out, k)
		}
	}
	return out, nil
}
func (m *MemoryStore) DeleteApiKey(ctx context.Context, id int64, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[id]
	if !ok || k.UserID != userID {
		return fmt.Errorf("not found")
	}
	delete(m.apiKeys, id)
	delete(m.apiKeysByKey, k.Key)
	return nil
}
func (m *MemoryStore) TouchApiKey(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.apiKeysByKey[key]
	if !ok {
		return nil
	}
	k := m.apiKeys[id]
	k.LastUsed = time.Now().Unix()
	m.apiKeys[id] = k
	return nil
}

func (m *MemoryStore) TryConsumeQuota(ctx context.Context, provider string, limit int) (bool, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	key := provider + ":" + day
	q, ok := m.quotas[key]
	if !ok {
		q = store.QuotaRow{Provider: provider, Day: day, Used: 0, Limit: limit}
	}
	if q.Limit != limit && q.Limit != 0 {
		limit = q.Limit
	}
	if q.Used >= limit {
		return false, q.Used, nil
	}
	q.Used++
	q.Limit = limit
	m.quotas[key] = q
	m.apiCalls = append(m.apiCalls, store.ApiCallRow{Provider: provider, Endpoint: "quota_consume", At: time.Now().Unix(), Cost: 1})
	return true, q.Used, nil
}

func (m *MemoryStore) GetQuota(ctx context.Context, provider string, day time.Time) (store.QuotaRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d := day
	if d.IsZero() {
		d = time.Now()
	}
	key := provider + ":" + d.Format("2006-01-02")
	q, ok := m.quotas[key]
	return q, ok
}

func (m *MemoryStore) SetQuotaLimit(ctx context.Context, provider string, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	key := provider + ":" + day
	q := m.quotas[key]
	q.Provider = provider
	q.Day = day
	q.Limit = limit
	m.quotas[key] = q
	return nil
}

func (m *MemoryStore) RecordApiCall(ctx context.Context, provider, endpoint string, cost int) error {
	m.mu.Lock()
	m.apiCalls = append(m.apiCalls, store.ApiCallRow{Provider: provider, Endpoint: endpoint, At: time.Now().Unix(), Cost: cost})
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) ListQuotas(ctx context.Context) ([]store.QuotaRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	out := make([]store.QuotaRow, 0, len(m.quotas))
	// issues #15/#21: только текущий день и вчерашний остаток — глубокая
	// история в UI выглядит как дубликаты (см. CleanupQuotaHistory).
	for _, q := range m.quotas {
		if q.Day != today && q.Day != yesterday {
			continue
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out, nil
}

// CleanupQuotaHistory — retention строк квот (issues #15/#21), зеркало
// postgres: удаляет записи старше keepDays дней.
func (m *MemoryStore) CleanupQuotaHistory(ctx context.Context, keepDays int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if keepDays < 0 {
		keepDays = 0
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	removed := 0
	for key, q := range m.quotas {
		if q.Day < cutoff {
			delete(m.quotas, key)
			removed++
		}
	}
	return removed, nil
}

func (m *MemoryStore) WriteAuditLog(ctx context.Context, userID *int64, action, entityType string, entityID *int64, details string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var id int64 = m.allocID()
	if m.auditLogs == nil {
		m.auditLogs = make([]store.AuditLogRow, 0)
	}
	m.auditLogs = append(m.auditLogs, store.AuditLogRow{ID: id, UserID: userID, Action: action, EntityType: entityType, EntityID: entityID, At: time.Now().Unix(), Details: details})
	return nil
}

func (m *MemoryStore) ListAuditLogs(ctx context.Context, limit int) ([]store.AuditLogRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]store.AuditLogRow, len(m.auditLogs))
	copy(out, m.auditLogs)
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) ListImports(ctx context.Context, limit int) ([]store.ImportRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]store.ImportRow, 0, len(m.imports))
	for p, at := range m.imports {
		out = append(out, store.ImportRow{ProviderID: p, At: at.Unix(), Status: "ok"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) ListImportLogs(ctx context.Context, limit int) ([]store.ImportLogRow, error) {
	return []store.ImportLogRow{}, nil
}

func (m *MemoryStore) ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error) {
	return m.ListTerminalsFiltered(ctx, limit, offset, sort, "asc", "")
}

func (m *MemoryStore) ListTerminalsFiltered(ctx context.Context, limit, offset int, sort, order, q string) ([]map[string]any, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q = strings.TrimSpace(strings.ToLower(q))
	filtered := make([]map[string]any, 0)
	for id, tr := range m.terminals {
		if tr.ValidTo != nil {
			continue
		}
		name := ""
		if m.terminalNames[id] != nil {
			name = m.terminalNames[id]["ru"]
			if name == "" {
				for _, v := range m.terminalNames[id] {
					name = v
					break
				}
			}
		}
		if q != "" {
			matched := strings.Contains(strings.ToLower(name), q)
			if !matched {
				for _, v := range m.terminalNames[id] {
					if strings.Contains(strings.ToLower(v), q) {
						matched = true
						break
					}
				}
			}
			if !matched {
				for _, a := range m.aliases[id] {
					if strings.Contains(strings.ToLower(a.Alias), q) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, map[string]any{"id": id, "name": name, "lat": tr.Lat, "lon": tr.Lon, "is_locked": tr.IsLocked, "place_id": tr.PlaceID, "valid_from": tr.ValidFrom, "valid_to": derefOrEmpty(tr.ValidTo)})
	}
	total := len(filtered)
	out := filtered
	dirDesc := strings.ToLower(order) == "desc"
	switch sort {
	case "name":
		sortSlice(out, func(a, b map[string]any) bool {
			av := fmt.Sprint(a["name"])
			bv := fmt.Sprint(b["name"])
			if dirDesc {
				return av > bv
			}
			return av < bv
		})
	case "is_locked":
		sortSlice(out, func(a, b map[string]any) bool {
			av := a["is_locked"].(bool)
			bv := b["is_locked"].(bool)
			if av == bv {
				return a["id"].(int64) < b["id"].(int64)
			}
			if dirDesc {
				return !av && bv
			}
			return av && !bv
		})
	default:
		sortSlice(out, func(a, b map[string]any) bool {
			av := a["id"].(int64)
			bv := b["id"].(int64)
			if dirDesc {
				return av > bv
			}
			return av < bv
		})
	}
	if offset >= len(out) {
		return []map[string]any{}, total, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], total, nil
}

func sortSlice[T any](s []T, less func(a, b T) bool) {
	sort.Slice(s, func(i, j int) bool { return less(s[i], s[j]) })
}

func derefOrEmpty[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func (m *MemoryStore) EnqueueJob(ctx context.Context, j store.JobRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.allocID()
	j.ID = id
	if j.State == "" {
		j.State = "pending"
	}
	j.CreatedAt = time.Now().Unix()
	j.NextRun = time.Now().Format(time.RFC3339)
	m.jobs[id] = j
	return id, nil
}

func (m *MemoryStore) ClaimNextJob(ctx context.Context) (*store.JobRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *store.JobRow
	for _, j := range m.jobs {
		if j.State != "pending" && j.State != "retry" {
			continue
		}
		if best == nil || j.CreatedAt < best.CreatedAt {
			cp := j
			best = &cp
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no jobs")
	}
	best.State = "running"
	best.Attempts++
	m.jobs[best.ID] = *best
	return best, nil
}

func (m *MemoryStore) MarkJobDone(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	// cancelled не перезаписывается (issue #22)
	if j.State == "cancelled" {
		return nil
	}
	j.State = "done"
	m.jobs[id] = j
	return nil
}

func (m *MemoryStore) MarkJobRetry(ctx context.Context, id int64, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	j.State = "retry"
	j.LastError = errMsg
	j.Attempts++
	backoff := time.Duration(j.Attempts*2) * time.Minute
	if backoff > 30*time.Minute {
		backoff = 30 * time.Minute
	}
	j.NextRun = time.Now().Add(backoff).Format(time.RFC3339)
	m.jobs[id] = j
	return nil
}

func (m *MemoryStore) MarkJobDead(ctx context.Context, id int64, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	j.State = "dead"
	j.LastError = errMsg
	m.jobs[id] = j
	return nil
}

func (m *MemoryStore) RecoverStuckJobs(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, j := range m.jobs {
		if j.State == "running" {
			j.State = "retry"
			j.LastError = "recovered: orphaned running after restart"
			m.jobs[id] = j
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) ResetJob(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %d не найден", id)
	}
	switch j.State {
	case "done":
		return fmt.Errorf("job %d уже завершён (done)", id)
	case "pending", "retry", "running", "dead", "cancelled":
	default:
		return fmt.Errorf("job %d в состоянии %q", id, j.State)
	}
	j.State = "pending"
	j.Attempts = 0
	j.LastError = ""
	m.jobs[id] = j
	return nil
}

// CancelJob — отмена задания из UI (issue #22), memory-зеркало Postgres.
func (m *MemoryStore) CancelJob(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %d не найден", id)
	}
	switch j.State {
	case "pending", "retry", "running":
	default:
		return fmt.Errorf("job %d уже завершён (%s)", id, j.State)
	}
	j.State = "cancelled"
	j.LastError = "cancelled by operator"
	m.jobs[id] = j
	return nil
}

func (m *MemoryStore) JobCancelled(ctx context.Context, id int64) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return false, fmt.Errorf("job %d не найден", id)
	}
	return j.State == "cancelled", nil
}

func (m *MemoryStore) ListJobs(ctx context.Context, limit int) ([]store.JobRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 20
	}
	out := make([]store.JobRow, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	m.mu.Lock()
	m.imports[providerID] = at
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	m.mu.Lock()
	m.imports[providerID] = at
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	at, ok := m.imports[providerID]
	if !ok {
		return ImportRow{}, false
	}
	return ImportRow{ProviderID: providerID, At: at.Unix()}, true
}
func (m *MemoryStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	m.mu.Lock()
	m.quality = append(m.quality, q)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) ClearQualityIssues(ctx context.Context, providerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.quality[:0]
	for _, q := range m.quality {
		if q.ProviderID != providerID {
			filtered = append(filtered, q)
		}
	}
	m.quality = filtered
	return nil
}
func (m *MemoryStore) ClearProviderData(ctx context.Context, providerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.stops {
		if v.ProviderID == providerID {
			delete(m.stops, k)
		}
	}
	for k, v := range m.routes {
		if v.ProviderID == providerID {
			delete(m.routes, k)
		}
	}
	for k, v := range m.carriers {
		if v.ProviderID == providerID {
			delete(m.carriers, k)
		}
	}
	for k, v := range m.trips {
		if v.ProviderID == providerID {
			delete(m.trips, k)
		}
	}
	filterST := m.stopTimes[:0]
	for _, st := range m.stopTimes {
		if trip, ok := m.trips[st.TripID]; ok && trip.ProviderID == providerID {
			continue
		}
		filterST = append(filterST, st)
	}
	m.stopTimes = filterST
	filterTr := m.transfers[:0]
	for _, tr := range m.transfers {
		from, ok1 := m.stops[tr.FromStopID]
		to, ok2 := m.stops[tr.ToStopID]
		if (ok1 && from.ProviderID == providerID) || (ok2 && to.ProviderID == providerID) {
			continue
		}
		filterTr = append(filterTr, tr)
	}
	m.transfers = filterTr
	for k, v := range m.stations {
		if v.PrimaryProvider == providerID {
			delete(m.stations, k)
		}
	}
	return nil
}
func (m *MemoryStore) UpsertService(ctx context.Context, s ServiceRow) error { return nil }
func (m *MemoryStore) UpsertServiceDay(ctx context.Context, d ServiceDayRow) error {
	return nil
}
func (m *MemoryStore) UpsertServiceException(ctx context.Context, e ServiceExceptionRow) error {
	return nil
}
func (m *MemoryStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }

func (m *MemoryStore) UpsertCity(ctx context.Context, c CityRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == 0 {
		for _, ex := range m.cities {
			if ex.Name == c.Name && ex.RegionCode == c.RegionCode {
				c.ID = ex.ID
				m.cities[c.ID] = c
				return c.ID, nil
			}
		}
		c.ID = m.allocID()
	}
	m.cities[c.ID] = c
	return c.ID, nil
}

func (m *MemoryStore) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == 0 {
		s.ID = m.allocID()
	}
	m.stations[s.ID] = s
	return s.ID, nil
}
func (m *MemoryStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == 0 {
		s.ID = m.allocID()
	}
	m.stops[s.ID] = s
	return s.ID, nil
}
func (m *MemoryStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	m.mu.Lock()
	m.stationCodes = append(m.stationCodes, c)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == 0 {
		c.ID = m.allocID()
	}
	m.carriers[c.ID] = c
	return c.ID, nil
}
func (m *MemoryStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.routes {
		if e.ProviderID == r.ProviderID && e.ExternalRouteCode == r.ExternalRouteCode {
			r.ID = id
			m.routes[id] = r
			return id, nil
		}
	}
	if r.ID == 0 {
		r.ID = m.allocID()
	}
	m.routes[r.ID] = r
	return r.ID, nil
}
func (m *MemoryStore) UpsertRouteRegion(ctx context.Context, routeID int64, region string) error {
	return nil
}
func (m *MemoryStore) UpsertTrip(ctx context.Context, t TripRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.trips {
		if e.RouteID == t.RouteID && e.ExternalTripCode == t.ExternalTripCode {
			t.ID = id
			m.trips[id] = t
			return id, nil
		}
	}
	if t.ID == 0 {
		t.ID = m.allocID()
	}
	m.trips[t.ID] = t
	return t.ID, nil
}
func (m *MemoryStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	m.mu.Lock()
	m.frequencies = append(m.frequencies, f)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertStopTime(ctx context.Context, st StopTimeRow) error {
	m.mu.Lock()
	m.stopTimes = append(m.stopTimes, st)
	m.mu.Unlock()
	return nil
}

// StopTimes — тестовый аксессор: копия stop_times для round-trip проверок
// персиста (match_score/match_method/is_provisional).
func (m *MemoryStore) StopTimes() []StopTimeRow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]StopTimeRow, len(m.stopTimes))
	copy(out, m.stopTimes)
	return out
}
func (m *MemoryStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	m.mu.Lock()
	m.transfers = append(m.transfers, tr)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertPlace(ctx context.Context, r PlaceRow, names map[string]string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == 0 {
		r.ID = m.allocID()
	}
	return r.ID, nil
}
func (m *MemoryStore) UpsertTerminal(ctx context.Context, r TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == 0 {
		r.ID = m.allocID()
	} else if r.ID >= m.nextID {
		m.nextID = r.ID + 1
	}
	if r.EnrichmentStatus == "" {
		r.EnrichmentStatus = "identity_only"
	}
	m.terminals[r.ID] = r
	if len(identifiers) > 0 {
		m.terminalIdents[r.ID] = append([]model.AdaptedIdentifier{}, identifiers...)
	}
	if m.terminalNames[r.ID] == nil {
		m.terminalNames[r.ID] = map[string]string{}
	}
	for k, v := range names {
		m.terminalNames[r.ID][k] = v
	}
	return r.ID, nil
}
func (m *MemoryStore) GetTerminal(ctx context.Context, id int64) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tr, ok := m.terminals[id]
	if !ok {
		return nil, fmt.Errorf("terminal %d not found", id)
	}
	name := ""
	if m.terminalNames[id] != nil {
		name = m.terminalNames[id]["ru"]
		if name == "" {
			for _, v := range m.terminalNames[id] {
				name = v
				break
			}
		}
	}
	return map[string]any{"id": id, "name": name, "lat": tr.Lat, "lon": tr.Lon, "is_locked": tr.IsLocked, "place_id": tr.PlaceID, "transport_types": tr.TransportTypes}, nil
}
func (m *MemoryStore) GetTerminalTags(ctx context.Context, id int64) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]string{}
	for k, v := range m.terminalTags[id] {
		out[k] = v
	}
	return out, nil
}
func (m *MemoryStore) SetTerminalTag(ctx context.Context, id int64, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.terminals[id]; !ok {
		return fmt.Errorf("terminal %d not found", id)
	}
	if m.terminalTags[id] == nil {
		m.terminalTags[id] = map[string]string{}
	}
	if value == "" {
		delete(m.terminalTags[id], key)
		return nil
	}
	m.terminalTags[id][key] = value
	return nil
}
func (m *MemoryStore) UpsertTerminalAlias(ctx context.Context, a store.TerminalAliasRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.terminals[a.TerminalID]; !ok {
		return fmt.Errorf("terminal %d not found", a.TerminalID)
	}
	if m.aliases[a.TerminalID] == nil {
		m.aliases[a.TerminalID] = map[string]store.TerminalAliasRow{}
	}
	m.aliases[a.TerminalID][a.Alias+"\x00"+a.Lang] = a
	return nil
}

func (m *MemoryStore) UpsertAttributeState(ctx context.Context, a store.AttributeStateRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	origin := a.Origin
	if origin == "" {
		origin = "live"
	}
	a.Origin = origin
	m.attrStates[a.EntityType+"\x00"+fmt.Sprint(a.EntityID)+"\x00"+a.Field+"\x00"+a.Source] = a
	return nil
}

func (m *MemoryStore) CreateSyncRun(ctx context.Context, r store.SyncRunRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.ID = m.allocID()
	if r.State == "" {
		r.State = "running"
	}
	m.syncRuns[r.ID] = r
	return r.ID, nil
}

func (m *MemoryStore) FinishSyncRun(ctx context.Context, id int64, state, summary string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.syncRuns[id]
	if !ok {
		return fmt.Errorf("sync run %d not found", id)
	}
	r.State = state
	r.Summary = summary
	m.syncRuns[id] = r
	return nil
}

func (m *MemoryStore) ListSyncRuns(ctx context.Context, limit int) ([]store.SyncRunRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	ids := make([]int64, 0, len(m.syncRuns))
	for id := range m.syncRuns {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]store.SyncRunRow, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.syncRuns[id])
	}
	return out, nil
}

func (m *MemoryStore) EnsureSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, c := range m.syncChunks {
		if c.RunID == runID && c.Entity == entity && c.ChunkKey == chunkKey {
			return id, nil
		}
	}
	id := m.allocID()
	m.syncChunks[id] = store.SyncChunkRow{ID: id, RunID: runID, Entity: entity, ChunkKey: chunkKey, State: "pending"}
	return id, nil
}

func (m *MemoryStore) GetSyncChunk(ctx context.Context, runID int64, entity, chunkKey string) (store.SyncChunkRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.syncChunks {
		if c.RunID == runID && c.Entity == entity && c.ChunkKey == chunkKey {
			return c, true
		}
	}
	return store.SyncChunkRow{}, false
}

func (m *MemoryStore) CompleteSyncChunk(ctx context.Context, id int64, planIDDone, state, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.syncChunks[id]
	if !ok {
		return fmt.Errorf("sync chunk %d not found", id)
	}
	c.State = state
	c.PlanIDDone = planIDDone
	c.LastError = lastError
	c.Attempts++
	m.syncChunks[id] = c
	return nil
}

func (m *MemoryStore) ListAttributeStates(ctx context.Context, entityType string, entityID int64) ([]store.AttributeStateRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []store.AttributeStateRow
	for _, a := range m.attrStates {
		if a.EntityType == entityType && a.EntityID == entityID {
			out = append(out, a)
		}
	}
	sortSlice(out, func(a, b store.AttributeStateRow) bool {
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.Source < b.Source
	})
	return out, nil
}

func (m *MemoryStore) skeletonRunIDs() map[int64]bool {
	out := map[int64]bool{}
	for id, r := range m.syncRuns {
		if r.Kind == "skeleton" {
			out[id] = true
		}
	}
	return out
}

func (m *MemoryStore) hasSkeletonState(id int64, skelRuns map[int64]bool) bool {
	for _, a := range m.attrStates {
		if a.EntityType == "terminal" && a.EntityID == id && a.SyncRunID != nil && skelRuns[*a.SyncRunID] {
			return true
		}
	}
	return false
}

func (m *MemoryStore) ListLegacyTerminals(ctx context.Context) ([]store.LegacyTerminalRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	skel := m.skeletonRunIDs()
	var out []store.LegacyTerminalRow
	for id, tr := range m.terminals {
		if m.hasSkeletonState(id, skel) {
			continue
		}
		name := ""
		enName := ""
		if m.terminalNames[id] != nil {
			name = m.terminalNames[id]["ru"]
			enName = m.terminalNames[id]["en"]
		}
		r := store.LegacyTerminalRow{ID: id, NameRu: name, NameEn: enName, Lat: tr.Lat, Lon: tr.Lon, Tz: tr.Tz, Identifiers: append([]model.AdaptedIdentifier{}, m.terminalIdents[id]...)}
		prefix := "terminal\x00" + fmt.Sprint(id) + "\x00"
		for key, v := range m.provenance {
			if strings.HasPrefix(key, prefix) {
				r.Votes = append(r.Votes, v)
			}
		}
		out = append(out, r)
	}
	sortSlice(out, func(a, b store.LegacyTerminalRow) bool { return a.ID < b.ID })
	return out, nil
}

func (m *MemoryStore) ListSkeletonTerminals(ctx context.Context) ([]store.SkeletonTerminalRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	skel := m.skeletonRunIDs()
	var out []store.SkeletonTerminalRow
	for id, tr := range m.terminals {
		if !m.hasSkeletonState(id, skel) {
			continue
		}
		name := ""
		if m.terminalNames[id] != nil {
			name = m.terminalNames[id]["ru"]
		}
		settlement := ""
		if m.terminalTags[id] != nil {
			settlement = m.terminalTags[id]["settlement"]
		}
		out = append(out, store.SkeletonTerminalRow{ID: id, NameRu: name, Lat: tr.Lat, Lon: tr.Lon, Settlement: settlement, Identifiers: append([]model.AdaptedIdentifier{}, m.terminalIdents[id]...), EnrichmentStatus: tr.EnrichmentStatus})
	}
	sortSlice(out, func(a, b store.SkeletonTerminalRow) bool { return a.ID < b.ID })
	return out, nil
}

func (m *MemoryStore) DeleteReviewQueue(ctx context.Context, entityType string, entityID int64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.reviewQueue[:0]
	for _, r := range m.reviewQueue {
		if r.EntityType == entityType && r.EntityID == entityID && (reason == "" || r.Reason == reason) {
			continue
		}
		kept = append(kept, r)
	}
	m.reviewQueue = kept
	return nil
}
func (m *MemoryStore) SaveProvenance(ctx context.Context, p model.Provenance) error {
	if !model.ValidProvenanceChannel(p.Channel) {
		return fmt.Errorf("save provenance: пустой/неканонический channel %q (канон: local_file/local_motis/transitous_prod/transitous_staging, план §3.3)", p.Channel)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at := p.ObservedAt.Unix()
	if at == 0 {
		at = time.Now().Unix()
	}
	m.provenance[p.EntityType+"\x00"+fmt.Sprint(p.EntityID)+"\x00"+p.Source] = store.ProvenanceVote{Source: p.Source, Confidence: p.Confidence, ObservedAt: at, Channel: p.Channel}
	return nil
}

// ListProvenanceChannels — зеркало postgres: entityID→channel, отсутствующий
// ID = нет записей (signal gate). При нескольких голосах канонизированный
// channel один и тот же (пишется только через валидацию), конфликта нет.
func (m *MemoryStore) ListProvenanceChannels(ctx context.Context, entityType string, ids []int64) (map[int64]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := entityType + "\x00"
	idSet := map[int64]bool{}
	for _, id := range ids {
		idSet[id] = true
	}
	out := map[int64]string{}
	for key, v := range m.provenance {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := key[len(prefix):]
		var entityID int64
		if i := strings.IndexByte(rest, 0); i >= 0 {
			if n, err := strconv.ParseInt(rest[:i], 10, 64); err == nil {
				entityID = n
			}
		}
		if entityID == 0 || !idSet[entityID] {
			continue
		}
		out[entityID] = v.Channel
	}
	return out, nil
}

// CheckProvenanceCompleteness — зеркало postgres gate (план §3.3/§6):
// живые routes/trips без provenance-записи с непустым channel.
func (m *MemoryStore) CheckProvenanceCompleteness(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	provOK := func(entityType string, id int64) bool {
		prefix := entityType + "\x00" + fmt.Sprint(id) + "\x00"
		for key, v := range m.provenance {
			if strings.HasPrefix(key, prefix) && v.Channel != "" {
				return true
			}
		}
		return false
	}
	out := []string{}
	for _, r := range m.routes {
		if r.ValidTo != nil {
			continue
		}
		if !provOK("route", r.ID) {
			out = append(out, "route "+r.ExternalRouteCode)
		}
	}
	for _, t := range m.trips {
		if t.ValidTo != nil {
			continue
		}
		if !provOK("trip", t.ID) {
			rn := ""
			if r, ok := m.routes[t.RouteID]; ok {
				rn = " (route " + r.ExternalRouteCode + ")"
			}
			out = append(out, "trip "+t.ExternalTripCode+rn)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (m *MemoryStore) SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.reviewQueue {
		if r.EntityType == e.EntityType && r.EntityID == e.EntityID && r.Reason == e.Reason {
			m.reviewQueue[i].Score = e.Score
			newFP := r.Fingerprint
			if e.Fingerprint != "" {
				newFP = e.Fingerprint
			}
			m.reviewQueue[i].Fingerprint = newFP
			// пере-детекция той же причины — живое наблюдение, а не возраст
			// (зеркало postgres-апсерта: observed_at=now(), count=count+1)
			m.reviewQueue[i].Count++
			// sticky (план §3.10, issue #8/#14): закрытая resolved/rejected
			// запись не пере-открывается тем же (или пустым) fingerprint
			if m.reviewQueue[i].State != "open" && (e.Fingerprint == "" || e.Fingerprint == r.Fingerprint) {
				return nil
			}
			m.reviewQueue[i].State = "open"
			return nil
		}
	}
	m.reviewQueue = append(m.reviewQueue, store.ReviewQueueRow{EntityType: e.EntityType, EntityID: e.EntityID, Reason: e.Reason, Score: e.Score, CreatedAt: time.Now().Unix(), Fingerprint: e.Fingerprint, State: "open", Count: 1})
	return nil
}

// ResolveReviewQueue — sticky-закрытие записи ревью вместо физического
// DELETE (issue #8/#14): повторная детекция того же конфликта не
// пере-открывает её.
func (m *MemoryStore) ResolveReviewQueue(ctx context.Context, entityType string, entityID int64, reason, state string) error {
	if state != "resolved" && state != "rejected" {
		return fmt.Errorf("resolve review queue: invalid state %q (resolved|rejected)", state)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.reviewQueue {
		if r.EntityType == entityType && r.EntityID == entityID && (reason == "" || r.Reason == reason) && r.State == "open" {
			m.reviewQueue[i].State = state
		}
	}
	return nil
}

func (m *MemoryStore) ListReviewQueue(ctx context.Context, limit int) ([]store.ReviewQueueRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]store.ReviewQueueRow, 0, len(m.reviewQueue))
	for _, r := range m.reviewQueue {
		if r.State != "" && r.State != "open" {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (m *MemoryStore) GetPlaceCity(ctx context.Context, placeID int64) (int64, string, error) {
	return placeID, "", nil
}
func (m *MemoryStore) ImportAdaptedRecords(ctx context.Context, records []model.AdaptedRecord) (int, int, error) {
	return len(records), 0, nil
}

func (m *MemoryStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	loadStart := time.Now()
	dayBase := time.Now().UTC().Truncate(24 * time.Hour)
	if !day.IsZero() {
		dayBase = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	}
	serviceDaysMatch := func(serviceDays string) bool {
		if serviceDays == "" {
			return true
		}
		want := int(dayBase.Weekday())
		for _, part := range strings.Split(serviceDays, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if n, err := strconv.Atoi(part); err == nil && n == want {
				return true
			}
		}
		return false
	}
	allow := map[string]bool{}
	if len(providers) > 0 {
		for _, p := range providers {
			allow[p] = true
		}
	}
	net := model.NewNetwork()
	stopIDMap := map[int64]string{}
	for _, s := range m.stations {
		if len(allow) > 0 && s.PrimaryProvider != "" && !allow[s.PrimaryProvider] {
			continue
		}
		id := s.ID
		_ = id
	}
	for id, st := range m.stops {
		if len(allow) > 0 && !allow[st.ProviderID] {
			continue
		}
		sid := string(rune(id))
		strID := st.ExternalCode
		if strID == "" {
			strID = st.Name
		}
		station, ok := m.stations[st.StationID]
		lat, lon := 0.0, 0.0
		if ok {
			lat, lon = station.Lat, station.Lon
		}
		ms := &model.Stop{ID: strID, ProviderID: st.ProviderID, Name: st.Name, Lat: lat, Lon: lon, Type: model.StopType(st.StopType)}
		if ms.Type == "" {
			ms.Type = classifier.Ru.Classify(st.Name, nil)
		}
		net.Stops[strID] = ms
		stopIDMap[id] = strID
		_ = sid
	}
	for _, r := range m.routes {
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		if r.ValidTo != nil {
			continue
		}
		mr := &model.Route{ID: r.ExternalRouteCode, ProviderID: r.ProviderID, ShortName: r.ShortName, LongName: r.LongName, Mode: model.Mode(r.Mode)}
		net.Routes[r.ExternalRouteCode] = mr
	}
	for _, t := range m.trips {
		if len(allow) > 0 && !allow[t.ProviderID] {
			continue
		}
		if t.ValidTo != nil {
			continue
		}
		route, ok := m.routes[t.RouteID]
		routeID := ""
		if ok {
			routeID = route.ExternalRouteCode
		}
		times := []model.StopTime{}
		for _, st := range m.stopTimes {
			if st.TripID != t.ID {
				continue
			}
			sid, ok := stopIDMap[st.StopID]
			if !ok {
				continue
			}
			times = append(times, model.StopTime{StopID: sid, Sequence: st.Seq, ArrivalSec: st.Arrival, DepartureSec: st.Departure, IsProvisional: st.IsProvisional})
		}
		sort.Slice(times, func(i, j int) bool { return times[i].Sequence < times[j].Sequence })
		if len(times) == 0 {
			continue
		}
		if !serviceDaysMatch(t.ServiceDays) {
			continue
		}
		mt := &model.Trip{ID: routeID + "|" + t.ExternalTripCode, RouteID: routeID, ProviderID: t.ProviderID, Mode: model.Mode(route.Mode), ServiceID: t.ServiceID, StopTimes: times}
		net.Trips[mt.ID] = mt
		for i := 0; i < len(times)-1; i++ {
			_ = i
		}
	}
	// transfers
	for _, tr := range m.transfers {
		from, ok1 := stopIDMap[tr.FromStopID]
		to, ok2 := stopIDMap[tr.ToStopID]
		if !ok1 || !ok2 {
			continue
		}
		net.Transfers = append(net.Transfers, model.Transfer{FromStopID: from, ToStopID: to, Minutes: tr.Minutes})
	}
	globalFareMem.mu.RLock()
	for k, v := range globalFareMem.fares {
		cp := v
		net.FareAttributes[k] = &model.FareAttribute{FareID: cp.FareID, Price: cp.Price, Currency: cp.Currency, Basis: cp.Basis}
		if cp.FareID != "" && net.FareAttributes[k].Currency == "" {
			net.FareAttributes[k].Currency = "RUB"
		}
		if net.FareAttributes[k].Basis == "" {
			net.FareAttributes[k].Basis = "fare"
		}
	}
	for k, v := range globalFareMem.zones {
		cp := v
		net.Zones[k] = &model.Zone{ID: cp.ZoneID, NameRu: cp.NameRu, NameEn: cp.NameEn}
	}
	routeCodeByID := map[int64]string{}
	for _, rr := range m.routes {
		routeCodeByID[rr.ID] = rr.ExternalRouteCode
	}
	for _, r := range globalFareMem.rules {
		var oz, dz *string
		if r.OriginZone != nil {
			oz = r.OriginZone
		}
		if r.DestinationZone != nil {
			dz = r.DestinationZone
		}
		rcode := routeCodeByID[r.RouteID]
		if rcode == "" {
			rcode = string(rune(r.RouteID))
		}
		net.FareRules = append(net.FareRules, model.FareRule{FareID: r.FareID, RouteID: rcode, OriginZone: oz, DestinationZone: dz})
	}
	for sid, zid := range globalFareMem.stopZones {
		if code, ok := stopIDMap[sid]; ok {
			net.StopZones[code] = zid
		}
	}
	globalFareMem.mu.RUnlock()
	// Build connections from stopTimes via trips
	for _, trip := range net.Trips {
		for i := 0; i < len(trip.StopTimes)-1; i++ {
			from := trip.StopTimes[i]
			to := trip.StopTimes[i+1]
			base := dayBase
			dep := base.Add(time.Duration(from.DepartureSec) * time.Second)
			arr := base.Add(time.Duration(to.ArrivalSec) * time.Second)
			if arr.Before(dep) {
				arr = arr.Add(24 * time.Hour)
			}
			net.Connections = append(net.Connections, model.Connection{
				TripID: trip.ID, ProviderID: trip.ProviderID, RouteID: trip.RouteID, Mode: trip.Mode,
				From: from.StopID, To: to.StopID, Departure: dep, Arrival: arr,
			})
		}
	}
	sort.Slice(net.Connections, func(i, j int) bool { return net.Connections[i].Departure.Before(net.Connections[j].Departure) })
	net.BuildIndexes()
	net.BuildWalkTransfers()
	net.BuildIndexes()
	sort.Slice(net.Connections, func(i, j int) bool {
		if net.Connections[i].Departure.Equal(net.Connections[j].Departure) {
			return net.Connections[i].Arrival.Before(net.Connections[j].Arrival)
		}
		return net.Connections[i].Departure.Before(net.Connections[j].Departure)
	})
	slog.Debug("LoadNetwork done", "stops", len(net.Stops), "trips", len(net.Trips), "connections", len(net.Connections), "transfers", len(net.Transfers), "elapsed_ms", time.Since(loadStart).Milliseconds())
	return net, nil
}

func init() {
	store.Register("memory", func(ctx context.Context, dsn string) (store.Store, error) {
		return NewMemoryStore(), nil
	})
}

// ——— identifier_schemes (зеркало postgres; seed — из миграции) ———

func (m *MemoryStore) ListIdentifierSchemes(ctx context.Context) ([]store.IdentifierSchemeRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]store.IdentifierSchemeRow, 0, len(m.schemes))
	for _, s := range m.schemes {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return out[i].System < out[j].System
		}
		return out[i].Code < out[j].Code
	})
	return out, nil
}

func (m *MemoryStore) UpsertIdentifierScheme(ctx context.Context, s store.IdentifierSchemeRow) error {
	if s.Code == "" || s.System == "" || s.DisplayName == "" {
		return fmt.Errorf("identifier scheme: code, system и display_name обязательны")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schemes[s.Code] = s
	return nil
}

func (m *MemoryStore) DeleteIdentifierScheme(ctx context.Context, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schemes[code]; !ok {
		return fmt.Errorf("тип кода %q не найден", code)
	}
	for _, ids := range m.terminalIdents {
		for _, id := range ids {
			if id.CodeType == code {
				return fmt.Errorf("тип кода %q используется у существующих идентификаторов — сначала убрать коды", code)
			}
		}
	}
	delete(m.schemes, code)
	return nil
}

func (m *MemoryStore) ListIdentifierSystems(ctx context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	out := []string{}
	for _, s := range m.schemes {
		if !seen[s.System] {
			seen[s.System] = true
			out = append(out, s.System)
		}
	}
	sort.Strings(out)
	return out, nil
}

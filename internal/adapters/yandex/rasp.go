package yandex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"strings"
	"sync"

	"travelmcp/internal/config"
	"travelmcp/internal/support/httpx"
)

// QuotaFunc — точка входа в общий счётчик api_quotas: атомарный инкремент
// на уровне БД, локальное состояние в процессе не ведётся (инвариант AGENTS).
// offline=true — работать только по дисковому кэшу, API не вызывать.
type QuotaFunc func(ctx context.Context, provider string, limit int) (ok bool, used int, err error)

// RaspScheduleItem — событие отправления из /schedule.
type RaspScheduleItem struct {
	Thread struct {
		UID           string `json:"uid"`
		Title         string `json:"title"`
		Number        string `json:"number"`
		TransportType string `json:"transport_type"`
		Carrier       struct {
			Code  int    `json:"code"`
			Title string `json:"title"`
		} `json:"carrier"`
	} `json:"thread"`
	Days      string  `json:"days"`
	Except    *string `json:"except_days"`
	Departure string  `json:"departure"`
	Arrival   *string `json:"arrival"`
	IsFuzzy   bool    `json:"is_fuzzy"`
}

// RaspSchedule — ответ /schedule для станции и даты.
type RaspSchedule struct {
	Station struct {
		Code        string `json:"code"`
		Title       string `json:"title"`
		StationType string `json:"station_type"`
		Transport   string `json:"transport_type"`
		Timezone    string `json:"timezone"`
	} `json:"station"`
	Pagination struct {
		Total  int `json:"total"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	} `json:"pagination"`
	Schedule []RaspScheduleItem `json:"schedule"`
}

// RaspThreadStop — стоп нитки: станция + времена.
type RaspThreadStop struct {
	Station struct {
		Code        string `json:"code"`
		Title       string `json:"title"`
		StationType string `json:"station_type"`
		Transport   string `json:"transport_type"`
	} `json:"station"`
	Arrival   *string `json:"arrival"`
	Departure *string `json:"departure"`
}

// RaspThread — ответ /thread по uid.
type RaspThread struct {
	UID           string `json:"uid"`
	Title         string `json:"title"`
	Number        string `json:"number"`
	TransportType string `json:"transport_type"`
	FuzzyTimes    bool   `json:"fuzzy_times"`
	Carrier       struct {
		Code  int    `json:"code"`
		Title string `json:"title"`
	} `json:"carrier"`
	Days  string           `json:"days"`
	Stops []RaspThreadStop `json:"stops"`
}

// Rasp — клиент Яндекс.Расписаний: cache-first (дисковый кэш валиден как
// собранные данные), miss → API через квоту, ответ сохраняется в кэш.
type Rasp struct {
	baseURL  string
	apiKey   string
	client   *httpx.Client
	cacheDir string
	quota    QuotaFunc
	offline  bool
	limit    int

	mu    sync.Mutex
	stats RaspStats
}

// RaspStats — сводка вызовов: что взято из кэша, что из API, что скипнуто.
type RaspStats struct {
	ScheduleCache int
	ScheduleAPI   int
	SchedulePages int
	ThreadCache   int
	ThreadAPI     int
	QuotaBlocked  int
}

func NewRasp(cfg config.Config, client *httpx.Client, cacheDir string, quota QuotaFunc, offline bool) *Rasp {
	base := cfg.Yandex.RaspURL
	if base == "" {
		base = "https://api.rasp.yandex.net/v3.0"
	}
	base = strings.TrimRight(base, "/")
	if quota == nil {
		quota = func(context.Context, string, int) (bool, int, error) { return false, 0, nil }
	}
	return &Rasp{
		baseURL:  base,
		apiKey:   cfg.Yandex.RaspKey,
		client:   client,
		cacheDir: cacheDir,
		quota:    quota,
		offline:  offline,
	}
}

// DefaultRaspQuotaLimit — суточный лимит вызовов Rasp API за прогон
// (согласован с DefaultQuotaLimit стора; 500 — легальный лимит Яндекса).
const DefaultRaspQuotaLimit = 500

func (r *Rasp) Stats() RaspStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// Schedule — события станции на дату с догоном пагинации. Кэш-файл
// schedule_<code>_<date>.json содержит одну страницу (как собирали
// скрипты); дополнительные страницы сохраняются как
// schedule_<code>_<date>_<offset>.json и мержатся в памяти.
func (r *Rasp) Schedule(ctx context.Context, stationCode, date string) (*RaspSchedule, error) {
	merged := &RaspSchedule{}
	if raw, ok := readJSONFile(r.schedulePath(stationCode, date, 0)); ok {
		if err := json.Unmarshal(raw, merged); err != nil {
			return nil, fmt.Errorf("rasp schedule cache %s: %w", stationCode, err)
		}
		r.mu.Lock()
		r.stats.ScheduleCache++
		r.mu.Unlock()
	} else if r.offline {
		return nil, fmt.Errorf("rasp: нет кэша расписания %s на %s (offline)", stationCode, date)
	} else {
		if err := r.fetchSchedule(ctx, merged, stationCode, date, 0); err != nil {
			return nil, err
		}
	}
	if merged.Pagination.Limit <= 0 {
		merged.Pagination.Limit = 100
	}
	for off := merged.Pagination.Limit; off < merged.Pagination.Total; off += merged.Pagination.Limit {
		extra := &RaspSchedule{}
		if raw, ok := readJSONFile(r.schedulePath(stationCode, date, off)); ok {
			if err := json.Unmarshal(raw, extra); err != nil {
				return nil, fmt.Errorf("rasp schedule cache %s/%d: %w", stationCode, off, err)
			}
			merged.Schedule = append(merged.Schedule, extra.Schedule...)
			r.mu.Lock()
			r.stats.ScheduleCache++
			r.stats.SchedulePages++
			r.mu.Unlock()
			continue
		}
		if r.offline {
			break
		}
		if err := r.fetchSchedule(ctx, extra, stationCode, date, off); err != nil {
			return nil, err
		}
		merged.Schedule = append(merged.Schedule, extra.Schedule...)
	}
	dedupeScheduleUIDs(merged)
	return merged, nil
}

func (r *Rasp) fetchSchedule(ctx context.Context, out *RaspSchedule, stationCode, date string, offset int) error {
	ok, _, err := r.quota(ctx, "yandex", DefaultRaspQuotaLimit)
	if err != nil || !ok {
		r.mu.Lock()
		r.stats.QuotaBlocked++
		r.mu.Unlock()
		if err != nil {
			return fmt.Errorf("rasp: quota: %w", err)
		}
		return fmt.Errorf("rasp: квота yandex исчерпана (schedule %s)", stationCode)
	}
	if err := r.get(ctx, "schedule", url.Values{
		"station": {stationCode}, "date": {date},
		"lang": {"ru_RU"}, "limit": {"100"}, "offset": {fmt.Sprint(offset)},
	}, out); err != nil {
		return err
	}
	r.mu.Lock()
	r.stats.ScheduleAPI++
	r.stats.SchedulePages++
	r.mu.Unlock()
	if offset == 0 {
		_ = writeJSONFile(r.schedulePath(stationCode, date, 0), out)
	} else {
		_ = writeJSONFile(r.schedulePath(stationCode, date, offset), out)
	}
	return nil
}

func dedupeScheduleUIDs(s *RaspSchedule) {
	seen := map[string]bool{}
	out := s.Schedule[:0]
	for _, it := range s.Schedule {
		if it.Thread.UID == "" || seen[it.Thread.UID] {
			continue
		}
		seen[it.Thread.UID] = true
		out = append(out, it)
	}
	s.Schedule = out
}

// TransportCompatibleRasp — совместимость rasp-типа нитки с канонным
// типом терминала (bus/train/flight/subway/...): suburban-электрички
// относятся к rail, plane — к flight.
func TransportCompatibleRasp(raspType, canonical string) bool {
	if raspType == "" || canonical == "" {
		return true
	}
	canonicalOf := map[string]string{
		"train": "rail", "suburban": "rail", "plane": "flight",
	}
	if c, ok := canonicalOf[raspType]; ok {
		raspType = c
	}
	return raspType == canonical
}

// Thread — полная нитка по uid: кэш → API (квота) → кэш.
func (r *Rasp) Thread(ctx context.Context, uid string) (*RaspThread, error) {
	path := filepath.Join(r.cacheDir, "thread_"+sanitizeUID(uid)+".json")
	if raw, ok := readJSONFile(path); ok {
		t := &RaspThread{}
		if err := json.Unmarshal(raw, t); err != nil {
			return nil, fmt.Errorf("rasp thread cache %s: %w", uid, err)
		}
		r.mu.Lock()
		r.stats.ThreadCache++
		r.mu.Unlock()
		return t, nil
	}
	if r.offline {
		return nil, fmt.Errorf("rasp: нет кэша нитки %s (offline)", uid)
	}
	ok, _, err := r.quota(ctx, "yandex", DefaultRaspQuotaLimit)
	if err != nil || !ok {
		r.mu.Lock()
		r.stats.QuotaBlocked++
		r.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("rasp: quota: %w", err)
		}
		return nil, fmt.Errorf("rasp: квота yandex исчерпана (thread %s)", uid)
	}
	t := &RaspThread{}
	if err := r.get(ctx, "thread", url.Values{"uid": {uid}, "lang": {"ru_RU"}}, t); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.stats.ThreadAPI++
	r.mu.Unlock()
	_ = writeJSONFile(path, t)
	return t, nil
}

func (r *Rasp) get(ctx context.Context, endpoint string, params url.Values, out any) error {
	if r.apiKey == "" {
		return fmt.Errorf("rasp: yandex.rasp_key пуст (env YANDEX_RASP_KEY)")
	}
	params.Set("apikey", r.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/"+endpoint, nil)
	if err != nil {
		return err
	}
	req.URL.RawQuery = params.Encode()
	resp, err := r.client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("rasp %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rasp %s: статус %d", endpoint, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("rasp %s: decode: %w", endpoint, err)
	}
	return nil
}

// NearestStation — резолв yandex_code станции по координате (Rasp API
// nearest_stations): для станций из Overpass, у которых нет кода Яндекса.
// Кэш nearest_<lat>_<lon>.json, miss → API через квоту.
func (r *Rasp) NearestStation(ctx context.Context, lat, lon float64) (string, error) {
	key := fmt.Sprintf("nearest_%.5f_%.5f", lat, lon)
	path := filepath.Join(r.cacheDir, sanitizeUID(key)+".json")
	type nearestResp struct {
		Stations []struct {
			Codes struct {
				YandexCode string `json:"yandex_code"`
				EsrCode    string `json:"esr_code"`
			} `json:"codes"`
			StationType string `json:"station_type"`
		} `json:"stations"`
	}
	if raw, ok := readJSONFile(path); ok {
		var cached nearestResp
		if err := json.Unmarshal(raw, &cached); err == nil {
			for _, st := range cached.Stations {
				if st.Codes.YandexCode != "" {
					return st.Codes.YandexCode, nil
				}
			}
		}
	}
	if r.offline {
		return "", fmt.Errorf("rasp: нет кэша nearest для %.4f,%.4f (offline)", lat, lon)
	}
	ok, _, err := r.quota(ctx, "yandex", DefaultRaspQuotaLimit)
	if err != nil || !ok {
		r.mu.Lock()
		r.stats.QuotaBlocked++
		r.mu.Unlock()
		if err != nil {
			return "", fmt.Errorf("rasp: quota: %w", err)
		}
		return "", fmt.Errorf("rasp: квота yandex исчерпана (nearest %.4f,%.4f)", lat, lon)
	}
	out := nearestResp{}
	if err := r.get(ctx, "nearest_stations", url.Values{
		"lat": {fmt.Sprintf("%.6f", lat)}, "lon": {fmt.Sprintf("%.6f", lon)},
		"lang": {"ru_RU"}, "distance": {"50"}, "limit": {"5"},
	}, &out); err != nil {
		return "", err
	}
	_ = writeJSONFile(path, out)
	for _, st := range out.Stations {
		if st.Codes.YandexCode != "" {
			return st.Codes.YandexCode, nil
		}
	}
	return "", fmt.Errorf("rasp nearest: станции с yandex_code рядом с %.4f,%.4f не найдено", lat, lon)
}

func (r *Rasp) schedulePath(stationCode, date string, offset int) string {
	name := fmt.Sprintf("schedule_%s_%s", sanitizeUID(stationCode), date)
	if offset > 0 {
		name += fmt.Sprintf("_%d", offset)
	}
	return filepath.Join(r.cacheDir, name+".json")
}

// sanitizeUID — имена кэш-файлов: только безопасные символы (uid Яндекса
// содержит латиницу/цифры/подчёркивания, но страхуемся).
func sanitizeUID(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func readJSONFile(path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

func writeJSONFile(path string, v any) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

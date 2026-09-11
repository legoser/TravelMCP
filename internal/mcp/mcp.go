package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
	"travelmcp/internal/store"
)

type App struct {
	plan      *planner.Planner
	registry  *providers.Registry
	gazetteer *geo.Gazetteer
	store     store.Store
	logger    *slog.Logger

	// Кэш сети из Store по дню: LoadNetwork на живой БД читает
	// миллионы stop_times (GTFS-СПб — 3.5M), построчный запрос на каждый
	// find_route недопустим. Сеть на день иммутабельна в рамках запросов,
	// но только пока не изменился канон (canonVersion): сбор
	// рейсов/скелета, правка терминалов — кэш сбрасывается.
	netMu  sync.RWMutex
	netDay string
	netV   uint64
	net    *model.Network
	netErr error
}

// canonVersion — пакет-уровневый счётчик изменений канона: бампается
// после сборов скелета/рейсов, правок терминалов, merge/reset. Кэш сети
// в App живёт, пока версия не изменилась — без сквозной проводки
// колбеков через worker/server.
var canonVersion atomic.Uint64

// BumpCanonVersion — сигнализировать, что канон изменился: следующий
// find_route перечитает сеть из Store.
func BumpCanonVersion() { canonVersion.Add(1) }

func New(plan *planner.Planner, registry *providers.Registry) *App {
	return NewWithStore(plan, registry, nil, nil)
}

func NewWithStore(plan *planner.Planner, registry *providers.Registry, st store.Store, logger *slog.Logger) *App {
	a := &App{plan: plan, registry: registry, store: st, logger: logger}
	gz, err := geo.DefaultGazetteer()
	if err == nil {
		// Канон Postgres — основной источник поселений; статические 102 записи
		// остаются базой, registry-провайдеры — fallback (реестр/synth).
		if st != nil {
			if src, ok := st.(geo.SettlementSource); ok {
				added, skipped, err := gz.LoadSettlements(context.Background(), src)
				if logger != nil {
					logger.Info("gazetteer: loaded settlements from store", "added", added, "skipped_non_plausible", skipped, "error", err)
				}
			}
		}
		for _, p := range registry.List() {
			if n, err := p.Network(); err == nil {
				stops := make([]*model.Stop, 0, len(n.Stops))
				for _, s := range n.Stops {
					stops = append(stops, s)
				}
				gz.AddStops(stops)
			}
		}
		a.gazetteer = gz
	}
	return a
}

func (a *App) Server() *server.MCPServer {
	srv := server.NewMCPServer("travelmcp", "0.1.0",
		server.WithLogging(),
	)

	findRoute := mcp.NewTool(
		"find_route",
		mcp.WithDescription("Найти мультимодальный маршрут общественным транспортом «дверь-в-дверь» между двумя точками. Точки задают координатами (from_lat/from_lon, to_lat/to_lon) либо населённым пунктом (from_place/to_place)."),
		mcp.WithNumber("from_lat", mcp.Description("Широта точки отправления")),
		mcp.WithNumber("from_lon", mcp.Description("Долгота точки отправления")),
		mcp.WithNumber("to_lat", mcp.Description("Широта точки назначения")),
		mcp.WithNumber("to_lon", mcp.Description("Долгота точки назначения")),
		mcp.WithString("from_place", mcp.Description("Населённый пункт отправления, например «Юрга» или «Пермь». Взамен from_lat/from_lon.")),
		mcp.WithString("to_place", mcp.Description("Населённый пункт назначения, например «Барнаул». Взамен to_lat/to_lon.")),
		mcp.WithString("departure", mcp.Description("Время отправления в формате RFC3339; по умолчанию — сейчас")),
		mcp.WithString("arrival", mcp.Description("Время прибытия в формате RFC3339 (альтернатива departure — быть в точке к этому времени)")),
		mcp.WithBoolean("allow_gap", mcp.Description("Разрешить gap/self-leg для непокрытых фрагментов (дверь-в-дверь всегда)")),
		mcp.WithString("preference", mcp.Description("Предпочтение: arrival (быстрее) или transfers (меньше пересадок), по умолчанию arrival")),
		mcp.WithNumber("max_walk_minutes", mcp.Description("Максимальная пешая доступность до остановки, мин (по умолчанию — из конфига сервера, 30)")),
		mcp.WithNumber("max_transfers", mcp.Description("Лимит пересадок; -1 — без ограничения. По умолчанию -1")),
		mcp.WithString("transit_modes", mcp.Description("Режимы транспорта через запятую: BUS,COACH,RAIL,SUBWAY,TRAM,FLIGHT,TAXI,CAR,BICYCLE,SCOOTER. Пусто — все режимы")),
	)
	srv.AddTool(findRoute, a.handleFindRoute)

	listProviders := mcp.NewTool(
		"list_providers",
		mcp.WithDescription("Перечислить подключённые источники данных и их состояние."),
	)
	srv.AddTool(listProviders, a.handleListProviders)

	return srv
}

func (a *App) handleFindRoute(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.logger != nil {
		a.logger.DebugContext(ctx, "find_route request", "args", req.GetArguments())
	}
	for _, k := range []string{"from_place", "to_place", "departure", "arrival", "preference"} {
		if v, ok := req.GetArguments()[k]; ok {
			if _, ok := v.(string); !ok {
				return mcp.NewToolResultError(fmt.Sprintf("%s: ожидается строка, получен %T %v", k, v, v)), nil
			}
		}
	}
	from, fromPlace, err := a.resolvePointWithPlace(ctx, req.GetArguments(), "from")
	if err != nil {
		if a.logger != nil {
			a.logger.WarnContext(ctx, "find_route bad args", "error", err, "args", req.GetArguments())
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, toPlace, err := a.resolvePointWithPlace(ctx, req.GetArguments(), "to")
	if err != nil {
		if a.logger != nil {
			a.logger.WarnContext(ctx, "find_route bad args", "error", err, "args", req.GetArguments())
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := model.SearchParams{
		Departure:    time.Now(),
		MaxTransfers: -1,
	}

	if v, ok := req.GetArguments()["departure"]; ok {
		s, ok := v.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("departure: ожидается строка RFC3339, получен %T %v", v, v)), nil
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("departure: ожидается RFC3339, получено %q: %v", s, err)), nil
		}
		params.Departure = t
	}
	if v, ok := req.GetArguments()["arrival"]; ok {
		s, ok := v.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("arrival: ожидается строка RFC3339, получен %T %v", v, v)), nil
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("arrival: ожидается RFC3339, получено %q: %v", s, err)), nil
		}
		params.Arrival = &t
	}
	if v, ok := req.GetArguments()["allow_gap"]; ok {
		switch x := v.(type) {
		case bool:
			params.AllowGap = x
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true", "1", "yes", "y":
				params.AllowGap = true
			case "false", "0", "no", "n", "":
				params.AllowGap = false
			default:
				return mcp.NewToolResultError(fmt.Sprintf("allow_gap: ожидается boolean, получено %q (допустимо true/false)", x)), nil
			}
		default:
			return mcp.NewToolResultError(fmt.Sprintf("allow_gap: ожидается boolean, получен %T %v", v, v)), nil
		}
	}
	if v, ok := req.GetArguments()["preference"]; ok {
		s, ok := v.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("preference: ожидается строка arrival/transfers, получен %T %v", v, v)), nil
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "transfers", "transfer":
			params.Preference = model.PreferenceTransfers
		case "arrival":
			params.Preference = model.PreferenceArrival
		default:
			return mcp.NewToolResultError(fmt.Sprintf("preference: ожидается \"arrival\" или \"transfers\", получено %q", s)), nil
		}
	} else {
		params.Preference = model.PreferenceTransfers
	}
	if _, exists := req.GetArguments()["max_walk_minutes"]; exists {
		v, ok := argFloatStrict(req.GetArguments(), "max_walk_minutes")
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("max_walk_minutes: ожидается число 0..180, получено %v (%T)", req.GetArguments()["max_walk_minutes"], req.GetArguments()["max_walk_minutes"])), nil
		}
		if v < 0 || v > 180 {
			return mcp.NewToolResultError(fmt.Sprintf("max_walk_minutes: ожидается 0..180, получено %.0f", v)), nil
		}
		params.MaxWalkMinutes = int(v)
	}
	if _, exists := req.GetArguments()["max_transfers"]; exists {
		v, ok := argFloatStrict(req.GetArguments(), "max_transfers")
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("max_transfers: ожидается число -1..20, получено %v (%T)", req.GetArguments()["max_transfers"], req.GetArguments()["max_transfers"])), nil
		}
		if v < -1 || v > 20 {
			return mcp.NewToolResultError(fmt.Sprintf("max_transfers: ожидается -1..20, получено %.0f", v)), nil
		}
		params.MaxTransfers = int(v)
	}
	if v, ok := req.GetArguments()["transit_modes"]; ok {
		s, ok := v.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("transit_modes: ожидается строка, получен %T %v", v, v)), nil
		}
		s = strings.TrimSpace(s)
		if s != "" {
			modes := model.ParseTransitModes(s)
			if len(modes) == 0 {
				return mcp.NewToolResultError(fmt.Sprintf("transit_modes: неизвестные режимы %q (допустимо BUS,COACH,RAIL,SUBWAY,TRAM,FLIGHT,TAXI,CAR,BICYCLE,SCOOTER)", s)), nil
			}
			params.AllowedModes = modes
		}
	}
	for _, k := range []string{"from_lat", "from_lon", "to_lat", "to_lon"} {
		if _, exists := req.GetArguments()[k]; exists {
			if _, ok := argFloatStrict(req.GetArguments(), k); !ok {
				return mcp.NewToolResultError(fmt.Sprintf("%s: ожидается число, получено %v (%T)", k, req.GetArguments()[k], req.GetArguments()[k])), nil
			}
		}
	}

	day := params.Departure
	if params.Arrival != nil {
		day = *params.Arrival
	}
	net, err := a.networkForDay(ctx, day)
	if err != nil {
		if a.logger != nil {
			a.logger.ErrorContext(ctx, "network build failed", "error", err)
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	if a.logger != nil {
		a.logger.DebugContext(ctx, "network ready", "stops", len(net.Stops), "trips", len(net.Trips), "connections", len(net.Connections))
		a.logger.InfoContext(ctx, "find_route search", "from", from, "to", to, "departure", params.Departure, "arrival", params.Arrival, "allowGap", params.AllowGap)
	}

	var journey *model.Journey
	if fromPlace != nil || toPlace != nil {
		var fp, tp *planner.PlaceHint
		if fromPlace != nil {
			fp = &planner.PlaceHint{Name: *fromPlace}
		}
		if toPlace != nil {
			tp = &planner.PlaceHint{Name: *toPlace}
		}
		journey, err = a.plan.PlanWithPlaces(net, from, to, params, fp, tp)
	} else {
		journey, err = a.plan.Plan(net, from, to, params)
	}
	if err != nil {
		if a.logger != nil {
			a.logger.WarnContext(ctx, "find_route no route", "error", err, "from", from, "to", to)
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	if a.logger != nil {
		a.logger.InfoContext(ctx, "find_route success", "departure", journey.Departure, "arrival", journey.Arrival, "legs", len(journey.Legs), "transfers", journey.Transfers, "alternatives", len(journey.Alternatives))
		a.logger.DebugContext(ctx, "journey legs", "legs", journey.Legs)
	}
	enrichFuzzyLegs(journey, net)
	normalizeJourneyTimezones(journey)

	return mcp.NewToolResultJSON(journey)
}

// normalizeJourneyTimezones — все времена легов/джорни в единый вид
// RFC3339 UTC: планировщик собирает леги из разных зон (walk — зона
// запроса, transit — dayBase UTC), смешение "+07:00"/"Z" в одном ответе
// путает потребителей (issue #7).
func normalizeJourneyTimezones(j *model.Journey) {
	if j == nil {
		return
	}
	for i := range j.Legs {
		j.Legs[i].Departure = j.Legs[i].Departure.UTC()
		j.Legs[i].Arrival = j.Legs[i].Arrival.UTC()
	}
	j.Departure = j.Departure.UTC()
	j.Arrival = j.Arrival.UTC()
	for a := range j.Alternatives {
		normalizeJourneyTimezones(&j.Alternatives[a])
	}
}

// enrichFuzzyLegs — контакты перевозчика в TimeHint fuzzy-легов
// (§5.4 fallback: время интерполировано, показываем «уточняйте у
// перевозчика» + телефон/сайт/адрес из канона).
func enrichFuzzyLegs(j *model.Journey, net *model.Network) {
	if j == nil {
		return
	}
	for i := range j.Legs {
		if j.Legs[i].TimeHint == "" {
			continue
		}
		if r := net.Routes[j.Legs[i].RouteID]; r != nil && r.CarrierID != "" {
			if c := net.Carriers[r.CarrierID]; c != nil {
				contact := c.Phone
				if c.InfoURL != "" {
					if contact != "" {
						contact += ", " + c.InfoURL
					} else {
						contact = c.InfoURL
					}
				}
				if c.Address != "" && contact == "" {
					contact = c.Address
				}
				if contact != "" {
					j.Legs[i].TimeHint = "время ориентировочное — уточняйте у перевозчика (" + c.Name + ": " + contact + ")"
				} else {
					j.Legs[i].TimeHint = "время ориентировочное — уточняйте у перевозчика (" + c.Name + ")"
				}
				continue
			}
		}
		j.Legs[i].TimeHint = "время ориентировочное — уточняйте у перевозчика"
	}
	for a := range j.Alternatives {
		enrichFuzzyLegs(&j.Alternatives[a], net)
	}
}

func (a *App) resolvePointWithPlace(ctx context.Context, args map[string]any, kind string) (model.Coords, *string, error) {
	place, hasPlace := argString(args, kind+"_place")
	hasPlace = hasPlace && strings.TrimSpace(place) != ""
	hasLatRaw := args[kind+"_lat"] != nil
	hasLonRaw := args[kind+"_lon"] != nil
	lat, hasLat := argFloat(args, kind+"_lat")
	lon, hasLon := argFloat(args, kind+"_lon")
	if hasLatRaw && !hasLat {
		return model.Coords{}, nil, fmt.Errorf("%s_lat: ожидается число, получено %v (%T)", kind, args[kind+"_lat"], args[kind+"_lat"])
	}
	if hasLonRaw && !hasLon {
		return model.Coords{}, nil, fmt.Errorf("%s_lon: ожидается число, получено %v (%T)", kind, args[kind+"_lon"], args[kind+"_lon"])
	}

	if hasPlace && (hasLat || hasLon) {
		return model.Coords{}, nil, fmt.Errorf("%s: укажите либо %s_place, либо %s_lat/%s_lon", kind, kind, kind, kind)
	}
	if hasPlace {
		if a.logger != nil {
			a.logger.DebugContext(ctx, "resolvePoint: georesolve", "kind", kind, "place", place, "gazetteer_entries", a.gazetteer.Len())
		}
		if a.gazetteer == nil {
			return model.Coords{}, nil, fmt.Errorf("%s: газетир недоступен", kind)
		}
		rr := a.gazetteer.ResolveDetailed(place)
		if !rr.Found {
			if a.logger != nil {
				a.logger.WarnContext(ctx, "resolvePoint: place not in gazetteer", "kind", kind, "place", place, "hint", "нет в каноне settlement-тегов и статических записях")
			}
			return model.Coords{}, nil, fmt.Errorf("%s: населённый пункт %q не найден", kind, place)
		}
		if a.logger != nil {
			a.logger.DebugContext(ctx, "resolvePoint: georesolve ok", "kind", kind, "place", place, "coords", rr.Coords, "method", rr.Method, "matched", rr.Matched)
		}
		return rr.Coords, &place, nil
	}
	if !hasLat || !hasLon {
		return model.Coords{}, nil, fmt.Errorf("%s: укажите %s_place или координаты %s_lat/%s_lon", kind, kind, kind, kind)
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return model.Coords{}, nil, fmt.Errorf("%s: координаты вне диапазона lat -90..90 lon -180..180", kind)
	}
	return model.Coords{Lat: lat, Lon: lon}, nil, nil
}

func (a *App) handleListProviders(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	statuses := a.registry.HealthStatuses()
	return mcp.NewToolResultJSON(statuses)
}

func (a *App) network() (*model.Network, error) {
	return a.networkForDay(context.Background(), time.Now())
}

// InvalidateNetworkCache — канон изменился (сбор скелета/рейсов,
// правка терминалов): следующий find_route перечитает LoadNetwork.
func (a *App) InvalidateNetworkCache() {
	BumpCanonVersion()
}

func (a *App) networkForDay(ctx context.Context, day time.Time) (*model.Network, error) {
	if a.logger != nil {
		a.logger.DebugContext(ctx, "networkForDay", "day", day, "store", a.store != nil, "providers", len(a.registry.List()))
	}
	dayKey := day.UTC().Truncate(24 * time.Hour).Format("2006-01-02")
	cv := canonVersion.Load()
	a.netMu.RLock()
	if a.net != nil && a.netDay == dayKey && a.netV == cv {
		n := a.net
		a.netMu.RUnlock()
		return n, nil
	}
	a.netMu.RUnlock()
	if a.store != nil {
		ids := make([]string, 0, len(a.registry.List())+2)
		ids = append(ids, "gov-registry", "yandex")
		for _, p := range a.registry.List() {
			ids = append(ids, p.ID())
		}
		storeStart := time.Now()
		if n, err := a.store.LoadNetwork(ctx, ids, day); err == nil && len(n.Stops) > 0 {
			if a.logger != nil {
				byProv := map[string]int{}
				for _, t := range n.Trips {
					byProv[t.ProviderID]++
				}
				a.logger.InfoContext(ctx, "network from store", "stops", len(n.Stops), "trips", len(n.Trips), "connections", len(n.Connections), "transfers", len(n.Transfers), "day", dayKey, "trips_by_provider", fmt.Sprint(byProv), "elapsed_ms", time.Since(storeStart).Milliseconds())
				a.logger.DebugContext(ctx, "promote: LoadNetwork done", "stops", len(n.Stops), "routes", len(n.Routes), "trips", len(n.Trips), "connections", len(n.Connections), "elapsed_ms", time.Since(storeStart).Milliseconds())
			}
			a.netMu.Lock()
			a.netDay, a.net, a.netErr = dayKey, n, nil
			a.netV = canonVersion.Load()
			a.netMu.Unlock()
			return n, nil
		} else if a.logger != nil {
			if err != nil {
				a.logger.WarnContext(ctx, "store LoadNetwork failed, fallback to registry", "error", err, "elapsed_ms", time.Since(storeStart).Milliseconds())
			} else {
				a.logger.WarnContext(ctx, "store LoadNetwork empty, fallback to registry", "stops", 0, "elapsed_ms", time.Since(storeStart).Milliseconds())
			}
		}
	}
	net := model.NewNetwork()
	for _, p := range a.registry.List() {
		var n *model.Network
		var err error
		switch prov := p.(type) {
		case *providers.Synth:
			n, err = prov.NetworkForDay(day)
		default:
			n, err = p.Network()
		}
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", p.ID(), err)
		}
		issues := model.ValidateNetwork(n)
		excluded := model.FilterExcludedStops(n, issues)

		stopRename := map[string]string{}
		for id := range n.Stops {
			if _, exists := net.Stops[id]; exists {
				stopRename[id] = p.ID() + ":" + id
			}
		}
		routeRename := map[string]string{}
		for id := range n.Routes {
			if _, exists := net.Routes[id]; exists {
				routeRename[id] = p.ID() + ":" + id
			}
		}
		tripRename := map[string]string{}
		for id := range n.Trips {
			if _, exists := net.Trips[id]; exists {
				tripRename[id] = p.ID() + ":" + id
			}
		}

		for id, s := range n.Stops {
			if excluded[id] {
				continue
			}
			newID := id
			if rn, ok := stopRename[id]; ok {
				newID = rn
			}
			cp := *s
			cp.ID = newID
			net.Stops[newID] = &cp
		}
		for id, r := range n.Routes {
			newID := id
			if rn, ok := routeRename[id]; ok {
				newID = rn
			}
			cp := *r
			cp.ID = newID
			net.Routes[newID] = &cp
		}
		for id, t := range n.Trips {
			skip := false
			for _, st := range t.StopTimes {
				if excluded[st.StopID] {
					skip = true
					break
				}
				if _, ok := stopRename[st.StopID]; ok {
				}
			}
			if skip {
				continue
			}
			newID := id
			if rn, ok := tripRename[id]; ok {
				newID = rn
			}
			cp := *t
			cp.ID = newID
			if rn, ok := routeRename[t.RouteID]; ok {
				cp.RouteID = rn
			}
			newST := make([]model.StopTime, len(t.StopTimes))
			for i, st := range t.StopTimes {
				ns := st
				if rn, ok := stopRename[st.StopID]; ok {
					ns.StopID = rn
				}
				newST[i] = ns
			}
			cp.StopTimes = newST
			net.Trips[newID] = &cp
		}
		for _, c := range n.Connections {
			if excluded[c.From] || excluded[c.To] {
				continue
			}
			nc := c
			if rn, ok := tripRename[c.TripID]; ok {
				nc.TripID = rn
			}
			if rn, ok := routeRename[c.RouteID]; ok {
				nc.RouteID = rn
			}
			if rn, ok := stopRename[c.From]; ok {
				nc.From = rn
			}
			if rn, ok := stopRename[c.To]; ok {
				nc.To = rn
			}
			if c.Arrival.Before(c.Departure) {
				continue
			}
			net.Connections = append(net.Connections, nc)
		}
		for _, tr := range n.Transfers {
			if excluded[tr.FromStopID] || excluded[tr.ToStopID] {
				continue
			}
			ntr := tr
			if rn, ok := stopRename[tr.FromStopID]; ok {
				ntr.FromStopID = rn
			}
			if rn, ok := stopRename[tr.ToStopID]; ok {
				ntr.ToStopID = rn
			}
			net.Transfers = append(net.Transfers, ntr)
		}
		for id, st := range n.Stations {
			if _, exists := net.Stations[id]; exists {
				continue
			}
			cp := *st
			net.Stations[id] = &cp
		}
		for id, svc := range n.Services {
			if _, exists := net.Services[id]; !exists {
				cp := *svc
				net.Services[id] = &cp
			}
		}
		for id, days := range n.ServiceDays {
			net.ServiceDays[id] = append(net.ServiceDays[id], days...)
		}
		for id, exs := range n.ServiceExceptions {
			net.ServiceExceptions[id] = append(net.ServiceExceptions[id], exs...)
		}
		for id, c := range n.Carriers {
			if _, exists := net.Carriers[id]; !exists {
				cp := *c
				net.Carriers[id] = &cp
			}
		}
		for id, ps := range n.ProviderStops {
			if _, exists := net.ProviderStops[id]; !exists {
				cp := *ps
				net.ProviderStops[id] = &cp
			}
		}
		for id, z := range n.Zones {
			if _, exists := net.Zones[id]; !exists {
				cp := *z
				net.Zones[id] = &cp
			}
		}
		for id, fa := range n.FareAttributes {
			if _, exists := net.FareAttributes[id]; !exists {
				cp := *fa
				net.FareAttributes[id] = &cp
			}
		}
		net.FareRules = append(net.FareRules, n.FareRules...)
		for k, v := range n.StopZones {
			if _, exists := net.StopZones[k]; !exists {
				net.StopZones[k] = v
			} else if rn, ok := stopRename[k]; ok {
				net.StopZones[rn] = v
			} else {
				net.StopZones[k] = v
			}
		}
	}
	sort.Slice(net.Connections, func(i, j int) bool { return net.Connections[i].Departure.Before(net.Connections[j].Departure) })
	net.BuildIndexes()
	if a.logger != nil {
		a.logger.DebugContext(ctx, "promote: network merged from registry", "providers", len(a.registry.List()), "stops", len(net.Stops), "routes", len(net.Routes), "trips", len(net.Trips), "connections", len(net.Connections), "transfers", len(net.Transfers))
	}
	return net, nil
}

func argNumber(args map[string]any, key string) (float64, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("%s: поле обязательно", key)
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case json.Number:
		return n.Float64()
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, fmt.Errorf("%s: не число: %w", key, err)
		}
		return f, nil
	}
	return 0, fmt.Errorf("%s: неподдерживаемый тип %T", key, v)
}

func argString(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func argFloat(args map[string]any, key string) (float64, bool) {
	f, ok := argFloatStrict(args, key)
	return f, ok
}

func argFloatStrict(args map[string]any, key string) (float64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return f, true
		}
		return 0, false
	}
	return 0, false
}

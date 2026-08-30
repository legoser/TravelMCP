package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
)

type App struct {
	plan      *planner.Planner
	registry  *providers.Registry
	gazetteer *geo.Gazetteer
}

func New(plan *planner.Planner, registry *providers.Registry) *App {
	a := &App{plan: plan, registry: registry}
	gz, err := geo.DefaultGazetteer()
	if err == nil {
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
		mcp.WithNumber("max_walk_minutes", mcp.Description("Максимальная пешая доступность до остановки, мин. По умолчанию 30")),
		mcp.WithNumber("max_transfers", mcp.Description("Лимит пересадок; -1 — без ограничения. По умолчанию -1")),
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
	from, err := a.resolvePoint(req.GetArguments(), "from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := a.resolvePoint(req.GetArguments(), "to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	params := model.SearchParams{
		Departure:    time.Now(),
		MaxTransfers: -1,
	}

	if v, ok := argString(req.GetArguments(), "departure"); ok {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return mcp.NewToolResultError("departure: ожидается RFC3339"), nil
		}
		params.Departure = t
	}
	if v, ok := argFloat(req.GetArguments(), "max_walk_minutes"); ok {
		params.MaxWalkMinutes = int(v)
	}
	if v, ok := argFloat(req.GetArguments(), "max_transfers"); ok {
		params.MaxTransfers = int(v)
	}

	net, err := a.network()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	journey, err := a.plan.Plan(net, from, to, params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultJSON(journey)
}

func (a *App) resolvePoint(args map[string]any, kind string) (model.Coords, error) {
	place, hasPlace := argString(args, kind+"_place")
	hasPlace = hasPlace && strings.TrimSpace(place) != ""
	lat, hasLat := argFloat(args, kind+"_lat")
	lon, hasLon := argFloat(args, kind+"_lon")

	if hasPlace && (hasLat || hasLon) {
		return model.Coords{}, fmt.Errorf("%s: укажите либо %s_place, либо %s_lat/%s_lon", kind, kind, kind, kind)
	}
	if hasPlace {
		if a.gazetteer == nil {
			return model.Coords{}, fmt.Errorf("%s: газетир недоступен", kind)
		}
		c, ok := a.gazetteer.Resolve(place)
		if !ok {
			return model.Coords{}, fmt.Errorf("%s: населённый пункт %q не найден", kind, place)
		}
		return c, nil
	}
	if !hasLat || !hasLon {
		return model.Coords{}, fmt.Errorf("%s: укажите %s_place или координаты %s_lat/%s_lon", kind, kind, kind, kind)
	}
	return model.Coords{Lat: lat, Lon: lon}, nil
}

func (a *App) handleListProviders(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	statuses := a.registry.HealthStatuses()
	return mcp.NewToolResultJSON(statuses)
}

func (a *App) network() (*model.Network, error) {
	net := model.NewNetwork()
	for _, p := range a.registry.List() {
		n, err := p.Network()
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", p.ID(), err)
		}
		for id, s := range n.Stops {
			if _, exists := net.Stops[id]; exists {
				return nil, fmt.Errorf("providers: конфликт остановки %s между источниками", id)
			}
			net.Stops[id] = s
		}
		for id, r := range n.Routes {
			if _, exists := net.Routes[id]; exists {
				return nil, fmt.Errorf("providers: конфликт маршрута %s между источниками", id)
			}
			net.Routes[id] = r
		}
		for id, t := range n.Trips {
			if _, exists := net.Trips[id]; exists {
				return nil, fmt.Errorf("providers: конфликт рейса %s между источниками", id)
			}
			net.Trips[id] = t
		}
		net.Connections = append(net.Connections, n.Connections...)
		net.Transfers = append(net.Transfers, n.Transfers...)
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
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

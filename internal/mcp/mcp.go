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

	"travelmcp/internal/model"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
)

type App struct {
	plan     *planner.Planner
	registry *providers.Registry
}

func New(plan *planner.Planner, registry *providers.Registry) *App {
	return &App{plan: plan, registry: registry}
}

func (a *App) Server() *server.MCPServer {
	srv := server.NewMCPServer("travelmcp", "0.1.0",
		server.WithLogging(),
	)

	findRoute := mcp.NewTool(
		"find_route",
		mcp.WithDescription("Найти мультимодальный маршрут общественным транспортом «дверь-в-дверь» между двумя точками."),
		mcp.WithNumber("from_lat", mcp.Required(), mcp.Description("Широта точки отправления")),
		mcp.WithNumber("from_lon", mcp.Required(), mcp.Description("Долгота точки отправления")),
		mcp.WithNumber("to_lat", mcp.Required(), mcp.Description("Широта точки назначения")),
		mcp.WithNumber("to_lon", mcp.Required(), mcp.Description("Долгота точки назначения")),
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
	fromLat, err := argNumber(req.GetArguments(), "from_lat")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromLon, err := argNumber(req.GetArguments(), "from_lon")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	toLat, err := argNumber(req.GetArguments(), "to_lat")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	toLon, err := argNumber(req.GetArguments(), "to_lon")
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

	journey, err := a.plan.Plan(net, model.Coords{Lat: fromLat, Lon: fromLon}, model.Coords{Lat: toLat, Lon: toLon}, params)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultJSON(journey)
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

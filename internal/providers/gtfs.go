package providers

import (
	"travelmcp/internal/adapters/gtfs"
	"travelmcp/internal/model"
)

const GTFSID = "gtfs"

type GTFS struct {
	adapter *gtfs.Adapter
}

func NewGTFS(path string) *GTFS { return &GTFS{adapter: gtfs.New(path)} }

func (p *GTFS) ID() string { return GTFSID }

func (p *GTFS) Health() HealthStatus {
	net, err := p.Network()
	if err != nil {
		return HealthStatus{Up: false, LastError: err.Error()}
	}
	issues := model.ValidateNetwork(net)
	return HealthStatus{Up: true, Records: len(net.Stops), Issues: len(issues)}
}

func (p *GTFS) Network() (*model.Network, error) { return p.adapter.Load() }

func (p *GTFS) Empty() bool { return p.adapter.Empty() }

func (p *GTFS) Capabilities() Capabilities {
	return Capabilities{Modes: []model.Mode{model.ModeBus, model.ModeTram, model.ModeRail}}
}

func (p *GTFS) NetworkForDay(day interface{}) (*model.Network, error) { return p.Network() }

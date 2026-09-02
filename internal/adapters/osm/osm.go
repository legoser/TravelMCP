package osm

import "travelmcp/internal/model"

const ProviderID = "osm"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Enrich(net *model.Network) {
	for _, s := range net.Stops {
		if s.Type == "" {
			s.Type = model.StopTypeStation
		}
	}
}

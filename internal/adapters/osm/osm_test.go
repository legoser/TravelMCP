package osm

import (
	"testing"

	"travelmcp/internal/model"
)

func TestEnrichEdge(t *testing.T) {
	a := New()
	net := model.NewNetwork()
	typed := &model.Stop{ID: "t1", Type: model.StopTypeAirport}
	blank := &model.Stop{ID: "t2"}
	net.Stops["t1"] = typed
	net.Stops["t2"] = blank
	a.Enrich(net)
	if typed.Type != model.StopTypeAirport {
		t.Fatalf("existing type must survive: %q", typed.Type)
	}
	if blank.Type != model.StopTypeStation {
		t.Fatalf("blank type must default to station: %q", blank.Type)
	}
	a.Enrich(model.NewNetwork())
}

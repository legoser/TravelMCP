package classifier

import (
	"strings"

	"travelmcp/internal/model"
)

type Classifier interface {
	Classify(name string, tags map[string]string) model.StopType
}

type DefaultClassifier struct{}

func (DefaultClassifier) Classify(name string, tags map[string]string) model.StopType {
	if v, ok := tags["aeroway"]; ok && strings.Contains(v, "aerodrome") {
		return model.StopTypeAirport
	}
	if v, ok := tags["amenity"]; ok && v == "bus_station" {
		return model.StopTypeHub
	}
	if v, ok := tags["public_transport"]; ok && v == "station" {
		return model.StopTypeStation
	}
	return model.StopTypeStation
}

type RuClassifier struct {
	DefaultClassifier
}

func (RuClassifier) Classify(name string, tags map[string]string) model.StopType {
	if inferred := model.InferStopType(name); inferred != model.StopTypeStation {
		return inferred
	}
	return DefaultClassifier{}.Classify(name, tags)
}

var Default Classifier = DefaultClassifier{}
var Ru Classifier = RuClassifier{}

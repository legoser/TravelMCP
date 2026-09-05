package pricing

import (
	"testing"
	"travelmcp/internal/model"
)

func TestCostForLegWalk(t *testing.T) {
	net := model.NewNetwork()
	leg := &model.Leg{Mode: model.ModeWalk, From: model.LegPoint{StopID: "a"}, To: model.LegPoint{StopID: "b"}}
	c := CostForLeg(net, leg)
	if c.Has {
		t.Fatalf("walk should have no cost, got %+v", c)
	}
}

func TestCostForLegFareExact(t *testing.T) {
	net := model.NewNetwork()
	net.FareAttributes["f1"] = &model.FareAttribute{FareID: "f1", Price: 100, Currency: "RUB", Basis: "fare"}
	net.FareRules = append(net.FareRules, model.FareRule{FareID: "f1", RouteID: "r1"})
	leg := &model.Leg{Mode: model.ModeBus, RouteID: "r1", From: model.LegPoint{StopID: "s1"}, To: model.LegPoint{StopID: "s2"}}
	c := CostForLeg(net, leg)
	if !c.Has || c.Amount != 100 || c.Basis != model.CostBasisFare || c.Method != "fare" {
		t.Fatalf("expected fare 100, got %+v", c)
	}
}

func TestCostForLegFareZoneMismatch(t *testing.T) {
	net := model.NewNetwork()
	net.FareAttributes["f1"] = &model.FareAttribute{FareID: "f1", Price: 200, Currency: "RUB", Basis: "fare"}
	origin := "zoneA"
	net.FareRules = append(net.FareRules, model.FareRule{FareID: "f1", RouteID: "r1", OriginZone: &origin})
	net.StopZones["s1"] = "zoneB"
	net.Stops["s1"] = &model.Stop{ID: "s1", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Lat: 55.1, Lon: 37.1}
	leg := &model.Leg{Mode: model.ModeBus, RouteID: "r1", From: model.LegPoint{StopID: "s1", Lat: 55, Lon: 37}, To: model.LegPoint{StopID: "s2", Lat: 55.1, Lon: 37.1}}
	c := CostForLeg(net, leg)
	if c.Basis != model.CostBasisEstimate {
		t.Fatalf("zone mismatch should fallback to estimate, got %+v", c)
	}
	if !c.Has || c.Amount < 50 {
		t.Fatalf("estimate should be >=50, got %+v", c)
	}
}

func TestCostForLegEstimateFallback(t *testing.T) {
	net := model.NewNetwork()
	net.Stops["s1"] = &model.Stop{ID: "s1", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Lat: 56, Lon: 37}
	leg := &model.Leg{Mode: model.ModeBus, RouteID: "unknown", From: model.LegPoint{StopID: "s1", Lat: 55, Lon: 37}, To: model.LegPoint{StopID: "s2", Lat: 56, Lon: 37}}
	c := CostForLeg(net, leg)
	if !c.Has {
		t.Fatalf("estimate should have cost")
	}
	if c.Basis != model.CostBasisEstimate || c.Currency != "RUB" {
		t.Fatalf("estimate basis/currency wrong %+v", c)
	}
	if c.Amount < 50 {
		t.Fatalf("amount too small %v", c.Amount)
	}
}

func TestEnrichJourneyTotal(t *testing.T) {
	net := model.NewNetwork()
	net.FareAttributes["f1"] = &model.FareAttribute{FareID: "f1", Price: 150, Currency: "RUB", Basis: "fare"}
	net.FareRules = append(net.FareRules, model.FareRule{FareID: "f1", RouteID: "r1"})
	net.Stops["s1"] = &model.Stop{ID: "s1", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Lat: 55.5, Lon: 37}
	net.Stops["s3"] = &model.Stop{ID: "s3", Lat: 56, Lon: 37}
	j := &model.Journey{
		Legs: []model.Leg{
			{Mode: model.ModeBus, RouteID: "r1", From: model.LegPoint{StopID: "s1", Lat: 55, Lon: 37}, To: model.LegPoint{StopID: "s2", Lat: 55.5, Lon: 37}},
			{Mode: model.ModeBus, RouteID: "r1", From: model.LegPoint{StopID: "s2", Lat: 55.5, Lon: 37}, To: model.LegPoint{StopID: "s3", Lat: 56, Lon: 37}},
		},
	}
	EnrichJourney(net, j)
	if j.Legs[0].Cost.Amount != 150 || j.Legs[1].Cost.Amount != 150 {
		t.Fatalf("both legs should be 150, got %v %v", j.Legs[0].Cost, j.Legs[1].Cost)
	}
	total, cur, has := TotalPrice(j)
	if !has || total != 300 || cur != "RUB" {
		t.Fatalf("total 300 RUB expected, got %v %s %v", total, cur, has)
	}
}

func TestTotalPriceEmpty(t *testing.T) {
	total, _, has := TotalPrice(nil)
	if has || total != 0 {
		t.Fatalf("nil journey should have no price")
	}
	j := &model.Journey{Legs: []model.Leg{{Mode: model.ModeWalk, From: model.LegPoint{StopID: "a"}, To: model.LegPoint{StopID: "b"}}}}
	EnrichJourney(model.NewNetwork(), j)
	total, _, has = TotalPrice(j)
	if has {
		t.Fatalf("walk only should have no price, got %v", total)
	}
}

func TestCurrencyValidation(t *testing.T) {
	if err := ValidateCurrency("RUB"); err != nil {
		t.Fatalf("RUB should be valid")
	}
	if err := ValidateCurrency("usd"); err != nil {
		t.Fatalf("usd should be valid (case insensitive)")
	}
	if err := ValidateCurrency("XYZ"); err == nil {
		t.Fatal("XYZ should be invalid")
	}
	if err := ValidateCurrency("RUR"); err == nil {
		t.Fatal("RUR should be invalid (not supported)")
	}
	if NormalizeCurrency(" rub ") != CurrencyRUB {
		t.Fatalf("normalize failed")
	}
}

func TestMixedCurrencyJourney(t *testing.T) {
	net := model.NewNetwork()
	net.FareAttributes["f1"] = &model.FareAttribute{FareID: "f1", Price: 100, Currency: "RUB", Basis: "fare"}
	net.FareAttributes["f2"] = &model.FareAttribute{FareID: "f2", Price: 10, Currency: "USD", Basis: "fare"}
	net.FareRules = append(net.FareRules, model.FareRule{FareID: "f1", RouteID: "r1"})
	net.FareRules = append(net.FareRules, model.FareRule{FareID: "f2", RouteID: "r2"})
	net.Stops["s1"] = &model.Stop{ID: "s1", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Lat: 55.5, Lon: 37}
	net.Stops["s3"] = &model.Stop{ID: "s3", Lat: 56, Lon: 37}
	j := &model.Journey{Legs: []model.Leg{
		{Mode: model.ModeBus, RouteID: "r1", From: model.LegPoint{StopID: "s1"}, To: model.LegPoint{StopID: "s2"}},
		{Mode: model.ModeBus, RouteID: "r2", From: model.LegPoint{StopID: "s2"}, To: model.LegPoint{StopID: "s3"}},
	}}
	EnrichJourney(net, j)
	if j.Legs[0].Cost.Currency != "RUB" || j.Legs[1].Cost.Currency != "USD" {
		t.Fatalf("currencies not preserved %v %v", j.Legs[0].Cost, j.Legs[1].Cost)
	}
	totals := TotalPriceByCurrency(j)
	if totals["RUB"] != 100 || totals["USD"] != 10 {
		t.Fatalf("totals by currency wrong %v", totals)
	}
	if len(totals) != 2 {
		t.Fatalf("expected 2 currencies")
	}
}

func TestDefaultCurrencyFallback(t *testing.T) {
	orig := DefaultCurrencyVar
	defer func() { DefaultCurrencyVar = orig }()
	Configure("EUR")
	if DefaultCurrency() != CurrencyEUR {
		t.Fatalf("default should be EUR")
	}
	net := model.NewNetwork()
	net.Stops["s1"] = &model.Stop{ID: "s1", Lat: 55, Lon: 37}
	net.Stops["s2"] = &model.Stop{ID: "s2", Lat: 56, Lon: 37}
	leg := &model.Leg{Mode: model.ModeBus, RouteID: "unknown", From: model.LegPoint{StopID: "s1", Lat: 55, Lon: 37}, To: model.LegPoint{StopID: "s2", Lat: 56, Lon: 37}}
	c := CostForLeg(net, leg)
	if c.Currency != "EUR" {
		t.Fatalf("estimate should use default EUR, got %s", c.Currency)
	}
}

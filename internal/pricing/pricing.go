package pricing

import (
	"log/slog"
	"travelmcp/internal/geo"
	"travelmcp/internal/model"
)

func pricePerKm(mode model.Mode) float64 {
	switch mode {
	case model.ModeFlight:
		return 8.0
	case model.ModeRail:
		return 3.5
	case model.ModeTram, model.ModeMetro:
		return 2.0
	default:
		return 2.5
	}
}

func estimatePrice(from, to *model.Stop, mode model.Mode) float64 {
	if from == nil || to == nil {
		return 0
	}
	d := geo.Haversine(from.Coordinates(), to.Coordinates())
	if d < 1 {
		d = 1
	}
	price := d * pricePerKm(mode)
	if price < 50 {
		price = 50
	}
	return float64(int(price + 0.5))
}

func CostForLeg(net *model.Network, leg *model.Leg) model.Cost {
	if leg.Mode == model.ModeWalk {
		slog.Debug("pricing: walk no cost", "from", leg.From.StopID, "to", leg.To.StopID)
		return model.Cost{Has: false}
	}
	if net != nil && len(net.FareAttributes) > 0 {
		for _, r := range net.FareRules {
			if r.RouteID != "" && r.RouteID != leg.RouteID {
				continue
			}
			if r.OriginZone != nil && *r.OriginZone != "" {
				if z, ok := net.StopZones[leg.From.StopID]; !ok || z != *r.OriginZone {
					continue
				}
			}
			if r.DestinationZone != nil && *r.DestinationZone != "" {
				if z, ok := net.StopZones[leg.To.StopID]; !ok || z != *r.DestinationZone {
					continue
				}
			}
			if fa, ok := net.FareAttributes[r.FareID]; ok {
				cur := CurrencyOrDefault(fa.Currency)
				slog.Debug("pricing: fare hit", "route", leg.RouteID, "fare", fa.FareID, "price", fa.Price, "currency", cur)
				return model.Cost{Has: true, Amount: fa.Price, Currency: cur, Basis: model.CostBasisFare, Method: "fare"}
			}
		}
	}
	fromS := net.Stops[leg.From.StopID]
	toS := net.Stops[leg.To.StopID]
	if fromS == nil || toS == nil {
		slog.Warn("pricing: stop missing, using leg coords", "from", leg.From.StopID, "to", leg.To.StopID)
		fromS = &model.Stop{Lat: leg.From.Lat, Lon: leg.From.Lon}
		toS = &model.Stop{Lat: leg.To.Lat, Lon: leg.To.Lon}
	}
	price := estimatePrice(fromS, toS, leg.Mode)
	cur := CurrencyOrDefault("")
	slog.Debug("pricing: estimate fallback", "route", leg.RouteID, "mode", leg.Mode, "price", price, "currency", cur)
	return model.Cost{Has: true, Amount: price, Currency: cur, Basis: model.CostBasisEstimate, Method: "estimate"}
}

func EnrichJourney(net *model.Network, j *model.Journey) {
	if j == nil || net == nil {
		return
	}
	currencies := map[string]bool{}
	for i := range j.Legs {
		c := CostForLeg(net, &j.Legs[i])
		if c.Has {
			c.Currency = CurrencyOrDefault(c.Currency)
		}
		j.Legs[i].Cost = c
		if c.Has {
			currencies[c.Currency] = true
		}
	}
	if len(currencies) > 1 {
		slog.Warn("pricing: mixed currencies in journey, total is per-leg", "currencies", currencies)
	}
	for i := range j.Alternatives {
		for k := range j.Alternatives[i].Legs {
			c := CostForLeg(net, &j.Alternatives[i].Legs[k])
			if c.Has {
				c.Currency = CurrencyOrDefault(c.Currency)
			}
			j.Alternatives[i].Legs[k].Cost = c
		}
	}
}

func TotalPrice(j *model.Journey) (float64, string, bool) {
	if j == nil {
		return 0, "", false
	}
	totals := TotalPriceByCurrency(j)
	if len(totals) == 0 {
		return 0, "", false
	}
	if len(totals) == 1 {
		for cur, total := range totals {
			return total, cur, true
		}
	}
	slog.Warn("pricing: TotalPrice called on mixed-currency journey, returning last currency", "totals", totals)
	var lastCur string
	var lastTotal float64
	for cur, total := range totals {
		lastCur = cur
		lastTotal = total
	}
	return lastTotal, lastCur, true
}

func TotalPriceByCurrency(j *model.Journey) map[string]float64 {
	totals := map[string]float64{}
	if j == nil {
		return totals
	}
	for _, l := range j.Legs {
		if l.Cost.Has {
			cur := CurrencyOrDefault(l.Cost.Currency)
			totals[cur] += l.Cost.Amount
		}
	}
	return totals
}

func Configure(defaultCurrency string) {
	if defaultCurrency != "" {
		if err := ValidateCurrency(defaultCurrency); err != nil {
			slog.Warn("pricing: invalid default currency, keeping previous", "requested", defaultCurrency, "err", err)
			return
		}
		DefaultCurrencyVar = NormalizeCurrency(defaultCurrency)
		slog.Info("pricing: default currency configured", "currency", DefaultCurrencyVar)
	}
}

package sync

import (
	"context"
	"log/slog"
	"testing"

	"travelmcp/internal/model"
	"travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

func codeTerms() ([]AttachTerminal, *float64, *float64) {
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.5, 86.5
	return []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Transport: "bus", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: "42001"}}},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Settlement: "бета", Transport: "bus", Source: "osm", GeomFinalized: true},
	}, &latA, &latB
}

func codeTrip(opA, opB string) []model.FlatTrip {
	a0, d0 := 0, 60
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.5, 86.5
	return []model.FlatTrip{{
		RouteReg: "42.22.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Альфа", OpCode: opA, Region: "42", Lat: &latA, Lon: &lonA, ArrMin: &a0, DepMin: &a0},
			{StopID: "b", Name: "Бета", OpCode: opB, Region: "42", Lat: &latB, Lon: &lonB, ArrMin: &d0, DepMin: &d0},
		},
	}}
}

func TestAttachCodeMatchVerified(t *testing.T) {
	terms, _, _ := codeTerms()
	farLat, farLon := 60.0, 100.0
	trips := codeTrip("42001", "")
	trips[0].Stops[0].Name = "Совсем другое название"
	trips[0].Stops[0].Lat, trips[0].Stops[0].Lon = &farLat, &farLon
	trips[0].Stops[1].Lat, trips[0].Stops[1].Lon = terms[1].Lat, terms[1].Lon
	rep, err := AttachTrips(context.Background(), AttachInput{
		Trips: trips, Terminals: terms, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(rep.Promoted) != 1 {
		t.Fatalf("код обязан давать verified вопреки имени и гео: promoted=%d staged=%d dead=%d",
			len(rep.Promoted), len(rep.Staged), len(rep.Dead))
	}
	if got := rep.Promoted[0].StopTimes[0].TerminalID; got != 1 {
		t.Fatalf("стоп с кодом 42001 обязан лечь на терминал 1, лег на %d", got)
	}
}

func seedCodeStore(t *testing.T, ms *memory.MemoryStore, terms []AttachTerminal) {
	t.Helper()
	ctx := context.Background()
	for _, term := range terms {
		lat, lon := 0.0, 0.0
		if term.Lat != nil {
			lat = *term.Lat
		}
		if term.Lon != nil {
			lon = *term.Lon
		}
		if _, err := ms.UpsertTerminal(ctx, store.TerminalRow{ID: term.ID, Lat: lat, Lon: lon, EnrichmentStatus: "enriched"}, map[string]string{"ru": term.Name}, term.Codes); err != nil {
			t.Fatalf("seed %d: %v", term.ID, err)
		}
	}
}

func TestPersistAttachesIdentifiers(t *testing.T) {
	terms, _, _ := codeTerms()
	ms := memory.NewMemoryStore()
	seedCodeStore(t, ms, terms)
	ctx := context.Background()
	rep, err := AttachTrips(ctx, AttachInput{
		Trips: codeTrip("42001", "42002"), Terminals: terms, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	sum, err := PersistAttachReport(ctx, ms, rep, "mintrans", slog.Default())
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if sum.Codes != 2 {
		t.Fatalf("codes=%d want 2 (42001 подтверждение + 42002 новый)", sum.Codes)
	}
}

func TestResyncByCodeWithoutGeo(t *testing.T) {
	terms, _, _ := codeTerms()
	ms := memory.NewMemoryStore()
	seedCodeStore(t, ms, terms)
	ctx := context.Background()
	rep, err := AttachTrips(ctx, AttachInput{
		Trips: codeTrip("42001", "42002"), Terminals: terms, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans", slog.Default()); err != nil {
		t.Fatalf("persist: %v", err)
	}
	bare := []AttachTerminal{
		{ID: 1, Name: "Альфа", Settlement: "альфа", Source: "osm",
			Codes: []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: "42001"}}},
		{ID: 2, Name: "Бета", Settlement: "бета", Source: "osm",
			Codes: []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: "42002"}}},
	}
	trips := codeTrip("42001", "42002")
	for i := range trips[0].Stops {
		trips[0].Stops[i].Lat, trips[0].Stops[i].Lon = nil, nil
		trips[0].Stops[i].Name = "Неизвестное название " + trips[0].Stops[i].StopID
	}
	rep2, err := AttachTrips(ctx, AttachInput{
		Trips: trips, Terminals: bare, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if len(rep2.Promoted) != 1 {
		t.Fatalf("ресинк по коду без гео обязан промоутить: promoted=%d staged=%d", len(rep2.Promoted), len(rep2.Staged))
	}
}

func TestIdentifierClashAcrossTerminals(t *testing.T) {
	ms := memory.NewMemoryStore()
	ctx := context.Background()
	seedCodeStore(t, ms, []AttachTerminal{
		{ID: 1, Name: "Альфа", Codes: []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: "42001"}}},
		{ID: 2, Name: "Бета"},
	})
	if owner, err := ms.AttachTerminalIdentifier(ctx, 2, model.AdaptedIdentifier{System: "mintrans", CodeType: "op_reg", Code: "42001"}); err != nil || owner != 1 {
		t.Fatalf("чужой код обязан вернуть владельца: owner=%d err=%v", owner, err)
	}
	if owner, err := ms.AttachTerminalIdentifier(ctx, 1, model.AdaptedIdentifier{System: "mintrans", CodeType: "op_reg", Code: "42001"}); err != nil || owner != 0 {
		t.Fatalf("свой код обязан подтверждаться тихо: owner=%d err=%v", owner, err)
	}
}

func TestPersistStaleCodeDivergenceToReview(t *testing.T) {
	terms, _, _ := codeTerms()
	ms := memory.NewMemoryStore()
	seedCodeStore(t, ms, terms)
	ctx := context.Background()
	if _, err := ms.AttachTerminalIdentifier(ctx, 1, model.AdaptedIdentifier{System: "mintrans", CodeType: "op_reg", Code: "42000"}); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	rep, err := AttachTrips(ctx, AttachInput{
		Trips: codeTrip("42001", "42002"), Terminals: terms, TrustRouteNK: true, Source: "mintrans",
		ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	sum, err := PersistAttachReport(ctx, ms, rep, "mintrans", slog.Default())
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if len(rep.Promoted) != 1 {
		t.Fatalf("stale-код не обязан ронять рейс: promoted=%d", len(rep.Promoted))
	}
	if sum.CodeClash == 0 {
		t.Fatal("stale-код обязан посчитаться расхождением")
	}
	reviews, _ := ms.ListReviewQueue(ctx, 100)
	found := false
	for _, r := range reviews {
		if r.Reason == "possible_merge" && r.EntityID == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale-код обязан уйти в review possible_merge: %+v", reviews)
	}
}

func TestResyncSameCodesNoDivergence(t *testing.T) {
	terms, _, _ := codeTerms()
	ms := memory.NewMemoryStore()
	seedCodeStore(t, ms, terms)
	ctx := context.Background()
	run := func() PersistSummary {
		rep, err := AttachTrips(ctx, AttachInput{
			Trips: codeTrip("42001", "42002"), Terminals: terms, TrustRouteNK: true, Source: "mintrans",
			ChurnThreshold: 0.2, MaxSpeedKmh: 200, ParamsFor: attachParamsFor,
		})
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		sum, err := PersistAttachReport(ctx, ms, rep, "mintrans", slog.Default())
		if err != nil {
			t.Fatalf("persist: %v", err)
		}
		return sum
	}
	run()
	if sum := run(); sum.CodeClash != 0 {
		t.Fatalf("повторный ресинк тех же кодов обязан молчать: %+v", sum)
	}
}

func TestPersistAlertFreezesWriteback(t *testing.T) {
	terms, _, _ := codeTerms()
	ms := memory.NewMemoryStore()
	seedCodeStore(t, ms, terms)
	ctx := context.Background()
	rep := AttachReport{In: 1, Alert: true,
		Promoted: []PromotableTrip{{RouteNK: "42.22.001", TripNK: "42.22.001|forward:1:0"}}}
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans", slog.Default()); err == nil {
		t.Fatal("alerted-отчёт обязан запрещать персист (заморозка накопления кодов)")
	}
	if _, ok := ms.FindRouteID(ctx, "mintrans", "42.22.001"); ok {
		t.Fatal("alerted-персист ничего не обязан писать")
	}
}

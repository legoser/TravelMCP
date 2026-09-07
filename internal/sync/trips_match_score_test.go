package sync

import (
	"context"
	"testing"

	"travelmcp/internal/model"
	memstore "travelmcp/internal/store/memory"
)

func TestPersistMatchScoreMethodRoundTrip(t *testing.T) {
	ctx := context.Background()
	ms := memstore.NewMemoryStore()
	seedCodeStore(t, ms, []AttachTerminal{
		{ID: 11, Name: "Альфа", Settlement: "альфа", Source: "osm", GeomFinalized: true},
		{ID: 12, Name: "Бета", Settlement: "бета", Source: "osm", GeomFinalized: true},
	})

	rep := AttachReport{In: 1,
		Promoted: []PromotableTrip{{
			RouteNK: "42.03.001", TripNK: "42.03.001|forward:1:1", RouteReg: "42.03.001",
			Direction: "forward", ServiceID: 1, Run: 1,
			IsSyntheticKey: true, WinnerSource: "mintrans:winter", WinnerPeriod: "winter",
			Carrier: "ТП",
			StopTimes: []MatchedStopTime{
				{Seq: 0, TerminalID: 11, StopID: "a", ArrivalS: 36000, DepartureS: 36000, MatchScore: 0.95, MatchMethod: "scorepair"},
				{Seq: 1, TerminalID: 12, StopID: "b", ArrivalS: 39600, DepartureS: 39600, MatchScore: 1, MatchMethod: "code", IsProvisional: true},
			},
		}},
	}
	if _, err := PersistAttachReport(ctx, ms, rep, "mintrans"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	sts := ms.StopTimes()
	if len(sts) != 2 {
		t.Fatalf("stop_times: 2, got %d", len(sts))
	}
	bySeq := map[int]struct {
		score       float64
		method      string
		provisional bool
	}{}
	for _, st := range sts {
		bySeq[st.Seq] = struct {
			score       float64
			method      string
			provisional bool
		}{scorePtr(st.MatchScore), st.MatchMethod, st.IsProvisional}
	}
	if bySeq[0].method != "scorepair" || bySeq[0].score != 0.95 {
		t.Fatalf("seq=0: score/method потеряны: %+v", bySeq[0])
	}
	if bySeq[1].method != "code" || bySeq[1].score != 1 {
		t.Fatalf("seq=1: score/method потеряны: %+v", bySeq[1])
	}
	if !bySeq[1].provisional {
		t.Fatal("seq=1: is_provisional потерян")
	}
}

func scorePtr(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func TestMatchStopsFillsScoreMethod(t *testing.T) {
	// matchStops обязан заполнять MatchScore/MatchMethod: код-матч — method=code,
	// гео+имя — method=scorepair (D-4 трассируемость решает attach, не персист)
	latA, lonA := 55.0, 86.0
	latB, lonB := 55.5, 86.5
	terms := []AttachTerminal{
		{ID: 1, Name: "Альфа", Lat: &latA, Lon: &lonA, Settlement: "альфа", Source: "osm", GeomFinalized: true,
			Codes: []model.AdaptedIdentifier{{System: "mintrans", CodeType: "op_reg", Code: "op77"}}},
		{ID: 2, Name: "Бета", Lat: &latB, Lon: &lonB, Settlement: "бета", Source: "osm", GeomFinalized: true},
	}
	trips := []model.FlatTrip{{
		RouteReg: "42.04.001", Direction: "forward", ServiceID: 1, Period: "winter",
		Stops: []model.FlatStop{
			{StopID: "a", Name: "Альфа", Region: "42", Lat: &latA, Lon: &lonA, ArrMin: intPtr(600), DepMin: intPtr(600)},
			{StopID: "b", Name: "Бета", Region: "42", Lat: &latB, Lon: &lonB, ArrMin: intPtr(700), DepMin: intPtr(700), OpCode: "op77"},
		},
	}}
	idx := buildMatchIndex(terms)
	matched, _, _, unmatched := matchStops(trips[0], idx, "mintrans",
		func(string) model.DensityClass { return model.DensityRural }, attachParamsFor)
	if len(unmatched) != 0 || len(matched) != 2 {
		t.Fatalf("matched=%d unmatched=%v", len(matched), unmatched)
	}
	for _, m := range matched {
		if m.MatchMethod == "" || m.MatchScore <= 0 {
			t.Fatalf("score/method обязаны заполняться: %+v", m)
		}
	}
}

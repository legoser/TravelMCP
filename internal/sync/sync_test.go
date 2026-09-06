package sync

import (
	"testing"
	"time"
)

func TestComputePlanIDStable(t *testing.T) {
	a := ComputePlanID("v1", "cfg", []string{"b", "a"})
	b := ComputePlanID("v1", "cfg", []string{"a", "b"})
	if a != b {
		t.Fatalf("plan_id должен быть детерминирован независимо от порядка входов")
	}
	if ComputePlanID("v1", "cfg", []string{"a"}) == a {
		t.Fatalf("смена входов обязана менять plan_id")
	}
	if !ChunkFresh(a, a) || ChunkFresh("", a) || ChunkFresh("x", a) {
		t.Fatalf("ChunkFresh: совпал — пропуск, иначе — перепрогон")
	}
}

func TestDiffIgnoresAuditFields(t *testing.T) {
	a := []CanonicalRow{{NaturalKey: "r1", Fields: map[string]string{"name": "X", "observed_at": "1", "sync_run_id": "7"}}}
	b := []CanonicalRow{{NaturalKey: "r1", Fields: map[string]string{"name": "X", "observed_at": "2", "sync_run_id": "8"}}}
	if d := DiffProjections(CanonicalProjection(a), CanonicalProjection(b)); !d.Empty() {
		t.Fatalf("diff по аудит-полям обязан быть пустым: %+v", d)
	}
	c := []CanonicalRow{{NaturalKey: "r1", Fields: map[string]string{"name": "Y"}}}
	if d := DiffProjections(CanonicalProjection(a), CanonicalProjection(c)); len(d.Changed) != 1 {
		t.Fatalf("изменение канонического поля обязано попасть в diff: %+v", d)
	}
}

func TestOverridesRoundtrip(t *testing.T) {
	in := []Override{{EntityType: "terminal", System: "osm", Code: "123", Field: "geom", Value: "1,2", ActorID: 5}}
	data, err := MarshalOverrides(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalOverrides(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Key() != in[0].Key() {
		t.Fatalf("roundtrip overrides сломан: %+v", out)
	}
	if _, err := UnmarshalOverrides([]byte(`[{"entity_type":"terminal"}]`)); err == nil {
		t.Fatalf("битый override обязан отвергаться")
	}
}

func TestElectField(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	actor := int64(9)
	skel := AttributeCandidate{Field: "geom", Value: "osm", Source: "osm", Confidence: 0.5, ObservedAt: old}
	other := AttributeCandidate{Field: "geom", Value: "yandex", Source: "yandex", Confidence: 0.55, ObservedAt: now, Origin: "live"}
	if w, _ := ElectField([]AttributeCandidate{other, skel}); w.Source != "osm" {
		t.Fatalf("geom: скелет/OSM перекрывает с повышенным весом, победил %s", w.Source)
	}
	manual := AttributeCandidate{Field: "name", Value: "ручная", Source: "yandex", Confidence: 0.1, ObservedAt: old, ActorID: &actor}
	auto := AttributeCandidate{Field: "name", Value: "авто", Source: "osm", Confidence: 0.9, ObservedAt: now, Origin: "live"}
	if w, _ := ElectField([]AttributeCandidate{auto, manual}); w.Value != "ручная" {
		t.Fatalf("ручная правка обязана быть sticky, победило %q", w.Value)
	}
	seed := AttributeCandidate{Field: "name", Value: "seed", Source: "yandex", Confidence: 0.9, ObservedAt: now, Origin: "seed"}
	liveLow := AttributeCandidate{Field: "name", Value: "live", Source: "osm", Confidence: 0.2, ObservedAt: old, Origin: "live"}
	if w, _ := ElectField([]AttributeCandidate{seed, liveLow}); w.Value != "live" {
		t.Fatalf("seed не голосует при живом конкуренте, победило %q", w.Value)
	}
	if r := MatchLegacy(0.1, 0.6); r.Matched || r.ReviewReason != "legacy_unmatched" {
		t.Fatalf("без пары — review_queue{legacy_unmatched}: %+v", r)
	}
}

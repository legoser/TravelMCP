package motis

import (
	"testing"
)

func TestAdaptedFromMatchJSONErrors(t *testing.T) {
	if _, err := AdaptedFromMatchJSON([]byte("{broken")); err == nil {
		t.Fatal("malformed JSON must return error, not panic")
	}
	if _, err := AdaptedFromMatchJSON([]byte(`{"name":"","id":""}`)); err == nil {
		t.Fatal("invalid match must return error, not panic")
	}
}

func TestAdaptedFromMatchJSONValid(t *testing.T) {
	rec, err := AdaptedFromMatchJSON([]byte(`{"type":"STOP","name":"Кемерово","id":"s1","lat":55.34,"lon":86.06,"score":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec.NameRu == "" && rec.NameEn == "" {
		t.Fatalf("adapted record has no name: %+v", rec)
	}
}

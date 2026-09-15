package main

import (
	"strings"
	"testing"
)

const validDataset = `{
 "source":"minstran_reestr","snapshot":"2026-01-15",
 "routes":[{"reg":"42.42.001","name":"Кемерово — Топки"}],
 "stops":[{"id":"s1","name":"Кемерово","region":"42","op_reg":"op1"}],
 "carriers":[],
 "schedules":[{"route":"42.42.001","direction":"forward","service_id":1,"stops":[{"stop":"s1","region":"42"}]}],
 "services":[{"id":1,"name":"ежедневно","start_date":"2026-01-01","end_date":"2026-12-31"}],
 "service_days":[{"service_id":1,"weekday":3}],
 "service_exceptions":[]
}`

func TestValidateDatasetContractEdge(t *testing.T) {
	if err := ValidateDatasetContract([]byte(validDataset)); err != nil {
		t.Fatalf("valid dataset must pass: %v", err)
	}
	mutate := func(old, new string) string { return strings.Replace(validDataset, old, new, 1) }
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"not json", "{", "неверный JSON"},
		{"missing field", strings.Replace(validDataset, `"schedules"`, `"timetable"`, 1), "отсутствует обязательное поле"},
		{"bad source", mutate(`"minstran_reestr"`, `"other"`), "source"},
		{"bad snapshot shape", mutate(`"2026-01-15"`, `"15.01.2026"`), "snapshot"},
		{"bad snapshot date", mutate(`"2026-01-15"`, `"2026-13-40"`), "snapshot"},
		{"bad reg", mutate(`"42.42.001"`, `"abc"`), "reg"},
		{"dup reg", mutate(`"name":"Кемерово — Топки"`, `"name":"Кемерово — Топки"},{"reg":"42.42.001","name":"дубль"`), "дубль reg"},
		{"empty route name", mutate(`"name":"Кемерово — Топки"`, `"name":""`), "name пусто"},
		{"dup stop", mutate(`"op_reg":"op1"`, `"op_reg":"op1"},{"id":"s1","name":"дубль","region":"42","op_reg":"op2"`), "дубль id"},
		{"bad region", mutate(`"region":"42"`, `"region":"7"`), "want NN"},
		{"empty op_reg", mutate(`"op_reg":"op1"`, `"op_reg":""`), "op_reg пусто"},
		{"lat out of range", mutate(`"op_reg":"op1"`, `"op_reg":"op1","lat":91.0`), "lat"},
		{"bad direction", mutate(`"forward"`, `"sideways"`), "direction"},
		{"unknown route ref", mutate(`"route":"42.42.001"`, `"route":"42.42.999"`), "нет в routes"},
		{"unknown stop ref", mutate(`"stop":"s1"`, `"stop":"sX"`), "нет в stops"},
		{"unknown service ref", mutate(`"service_id":1`, `"service_id":9`), "нет в services"},
		{"bad weekday", mutate(`"weekday":3`, `"weekday":7`), "weekday"},
		{"empty carrier name", mutate(`"carriers":[]`, `"carriers":[{"name":""}]`), "carriers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDatasetContract([]byte(tc.raw))
			if err == nil {
				t.Fatal("want contract error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must mention %q", err, tc.want)
			}
		})
	}
}

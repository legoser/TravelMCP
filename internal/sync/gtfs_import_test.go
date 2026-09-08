package sync

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"travelmcp/internal/model"
	memstore "travelmcp/internal/store/memory"
)

func writeTestFeed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feed.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	files := map[string]string{
		"agency.txt":         "agency_id,agency_name,agency_url\norgp,ORG,http://x\n",
		"stops.txt":          "stop_id,stop_code,stop_name,stop_lat,stop_lon,location_type,transport_type\n100,100,\"ПЛОЩАДЬ ЛЕНИНА\",59.93,30.36,0,bus\n101,101,\"НЕВСКИЙ ПРОСПЕКТ\",59.935,30.33,0,bus\n",
		"routes.txt":         "route_id,agency_id,route_short_name,route_type\nR1,orgp,7,3\n",
		"calendar.txt":       "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n5,1,1,1,1,1,0,0,20260101,20261231\n",
		"calendar_dates.txt": "service_id,date,exception_type\n5,20260601,1\n",
		"trips.txt":          "trip_id,route_id,service_id,direction_id\nT1,R1,5,0\nT2,R1,5,1\n",
		"stop_times.txt":     "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,06:00:00,06:00:00,100,0\nT1,06:10:00,06:10:00,101,1\nT2,07:00:00,07:00:00,101,0\nT2,07:10:00,07:10:00,100,1\n",
		"frequencies.txt":    "trip_id,start_time,end_time,headway_secs,exact_times\nT2,08:00:00,20:00:00,600,0\n",
	}
	for name, body := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportGtfsFeedMemoryRoundtrip(t *testing.T) {
	path := writeTestFeed(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stt, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(f, stt.Size())
	if err != nil {
		t.Fatal(err)
	}
	ms := memstore.NewMemoryStore()
	ctx := context.Background()
	stats, err := ImportGtfsFeed(ctx, ms, zr)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stops != 2 || stats.Routes != 1 || stats.Services != 1 || stats.Trips != 2 || stats.StopTimes != 4 || stats.Frequencies != 1 || stats.Exceptions != 1 {
		t.Fatalf("счётчики импорта: %+v", stats)
	}
	// идемпотентность: повторный импорт не дублирует
	f.Seek(0, 0)
	zr2, err := zip.NewReader(f, stt.Size())
	if err != nil {
		t.Fatal(err)
	}
	stats2, err := ImportGtfsFeed(ctx, ms, zr2)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Stops != 0 || stats2.Routes != 0 || stats2.Trips != 0 {
		t.Fatalf("повторный импорт обязан быть no-op по NK: %+v", stats2)
	}
	// стоп-терминалы получили gtfs-идентификаторы
	if id, ok := ms.ListTerminalIDByCode(ctx, "gtfs", "100"); !ok || id == 0 {
		t.Fatal("стоп 100 обязан стать терминалом с gtfs:stop_code")
	}
	_ = model.ModeBus
}

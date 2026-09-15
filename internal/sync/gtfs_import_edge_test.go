package sync

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	memstore "travelmcp/internal/store/memory"
)

func zipFeed(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func minimalFeed() map[string]string {
	return map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url\norgp,ORG,http://x\n",
		"stops.txt":      "stop_id,stop_code,stop_name,stop_lat,stop_lon\n100,100,\"ПЛОЩАДЬ\",59.93,30.36\n",
		"routes.txt":     "route_id,agency_id,route_short_name,route_type\nR1,orgp,7,3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n5,1,1,1,1,1,0,0,20260101,20261231\n",
		"trips.txt":      "trip_id,route_id,service_id\nT1,R1,5\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,06:00:00,06:00:00,100,0\n",
	}
}

func TestImportGtfsMissingFile(t *testing.T) {
	feed := minimalFeed()
	delete(feed, "stops.txt")
	_, err := ImportGtfsFeed(context.Background(), memstore.NewMemoryStore(), zipFeed(t, feed))
	if err == nil {
		t.Fatal("missing stops.txt must fail loud")
	}
	if got := err.Error(); !strings.Contains(got, "stops.txt") {
		t.Fatalf("error must name the file: %q", got)
	}
}

func TestImportGtfsEmptyZip(t *testing.T) {
	_, err := ImportGtfsFeed(context.Background(), memstore.NewMemoryStore(), zipFeed(t, map[string]string{}))
	if err == nil {
		t.Fatal("empty feed must fail loud")
	}
}

func TestImportGtfsDanglingRefsSkipped(t *testing.T) {
	feed := minimalFeed()
	feed["stop_times.txt"] = "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
		"T1,06:00:00,06:00:00,100,0\n" +
		"T9,06:05:00,06:05:00,100,0\n" +
		"T1,06:10:00,06:10:00,999,1\n"
	stats, err := ImportGtfsFeed(context.Background(), memstore.NewMemoryStore(), zipFeed(t, feed))
	if err != nil {
		t.Fatalf("dangling refs must be skipped, not fatal: %v", err)
	}
	if stats.StopTimes != 1 {
		t.Fatalf("StopTimes=%d, want 1", stats.StopTimes)
	}
	if stats.SkippedStopTimes != 2 {
		t.Fatalf("SkippedStopTimes=%d, want 2", stats.SkippedStopTimes)
	}
}

func TestImportGtfsEmptyStopIDSkipped(t *testing.T) {
	feed := minimalFeed()
	feed["stops.txt"] = "stop_id,stop_code,stop_name,stop_lat,stop_lon\n100,100,\"ПЛОЩАДЬ\",59.93,30.36\n,,\"БЕЗ ID\",59.93,30.36\n"
	stats, err := ImportGtfsFeed(context.Background(), memstore.NewMemoryStore(), zipFeed(t, feed))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stops != 1 {
		t.Fatalf("Stops=%d, want 1 (empty id skipped)", stats.Stops)
	}
}

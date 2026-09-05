package gtfs

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

func TestGTFSAdapterEmptyPath(t *testing.T) {
	a := New("")
	_, err := a.Load()
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestGTFSAdapterMissingFile(t *testing.T) {
	a := New("/nonexistent/path.zip")
	_, err := a.Load()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestGTFSAdapterInvalidZip(t *testing.T) {
	f, err := os.CreateTemp("", "bad*.zip")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not a zip")
	f.Close()
	defer os.Remove(f.Name())
	a := New(f.Name())
	_, err = a.Load()
	if err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

func TestGTFSAdapterMinimalValid(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	// minimal stops
	w, _ := zw.Create("stops.txt")
	w.Write([]byte("stop_id,stop_name,stop_lat,stop_lon\ns1,Stop 1,55.0,37.0\n"))
	w2, _ := zw.Create("routes.txt")
	w2.Write([]byte("route_id,route_short_name,route_long_name,route_type\nr1,1,Route 1,3\n"))
	w3, _ := zw.Create("trips.txt")
	w3.Write([]byte("trip_id,route_id,service_id\nt1,r1,1\n"))
	w4, _ := zw.Create("stop_times.txt")
	w4.Write([]byte("trip_id,arrival_time,departure_time,stop_id,stop_sequence\nt1,08:00:00,08:00:00,s1,1\n"))
	zw.Close()
	f, err := os.CreateTemp("", "valid*.zip")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(buf.Bytes())
	f.Close()
	defer os.Remove(f.Name())
	a := New(f.Name())
	net, err := a.Load()
	if err != nil {
		t.Fatalf("load valid zip: %v", err)
	}
	if len(net.Stops) != 1 || len(net.Routes) != 1 {
		t.Fatalf("expected 1 stop and 1 route, got %d stops %d routes", len(net.Stops), len(net.Routes))
	}
}

func TestGTFSAdapterMissingOptionalFiles(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, _ := zw.Create("stops.txt")
	w.Write([]byte("stop_id,stop_name,stop_lat,stop_lon\ns1,Stop 1,55.0,37.0\n"))
	zw.Close()
	f, err := os.CreateTemp("", "partial*.zip")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(buf.Bytes())
	f.Close()
	defer os.Remove(f.Name())
	a := New(f.Name())
	net, err := a.Load()
	if err != nil {
		t.Fatalf("load partial zip should not error: %v", err)
	}
	if len(net.Stops) != 1 {
		t.Fatalf("expected 1 stop")
	}
	if len(net.Routes) != 0 {
		t.Fatalf("routes should be empty")
	}
}

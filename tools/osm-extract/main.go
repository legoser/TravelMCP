package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"

	"github.com/qedus/osmpbf"
)

type station struct {
	ID   int64             `json:"id"`
	Kind string            `json:"kind"`
	Name string            `json:"name,omitempty"`
	Tags map[string]string `json:"tags,omitempty"`
	Lat  float64           `json:"lat,omitempty"`
	Lon  float64           `json:"lon,omitempty"`
}

func tagFilter(tags map[string]string) bool {
	if v, ok := tags["amenity"]; ok && v == "bus_station" {
		return true
	}
	if v, ok := tags["building"]; ok && v == "bus_station" {
		return true
	}
	if v, ok := tags["public_transport"]; ok && v == "station" {
		if _, hasRail := tags["railway"]; !hasRail {
			return true
		}
	}
	if v, ok := tags["public_transport"]; ok && v == "platform" {
		return tags["bus"] == "yes" || tags["highway"] == "bus_stop"
	}
	if v, ok := tags["highway"]; ok && v == "bus_stop" {
		return true
	}
	return false
}

func keepTags(tags map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"name", "operator", "ref", "highway", "public_transport", "amenity", "building", "bus", "railway"} {
		if v, ok := tags[k]; ok {
			out[k] = v
		}
	}
	return out
}

func main() {
	inPath := flag.String("in", "", "путь к PBF")
	outPath := flag.String("out", "", "путь к JSON")
	flag.Parse()

	if *inPath == "" || *outPath == "" {
		log.Fatal("укажите -in и -out")
	}

	waysLocs := map[int64][2]float64{}
	doublePass := true
	if doublePass {
		f, err := os.Open(*inPath)
		if err != nil {
			log.Fatal(err)
		}
		d := osmpbf.NewDecoder(f)
		if err := d.Start(runtime.GOMAXPROCS(-1)); err != nil {
			log.Fatalf("pass1 start: %v", err)
		}
		need := map[int64]bool{}
		for {
			if v, err := d.Decode(); err == io.EOF {
				break
			} else if err != nil {
				log.Fatalf("pass1 decode: %v", err)
			} else if w, ok := v.(*osmpbf.Way); ok {
				if tagFilter(w.Tags) {
					for _, n := range w.NodeIDs {
						need[n] = true
					}
				}
			}
		}
		f.Close()

		f2, err := os.Open(*inPath)
		if err != nil {
			log.Fatal(err)
		}
		d2 := osmpbf.NewDecoder(f2)
		if err := d2.Start(runtime.GOMAXPROCS(-1)); err != nil {
			log.Fatalf("pass2 start: %v", err)
		}
		for {
			if v, err := d2.Decode(); err == io.EOF {
				break
			} else if err != nil {
				log.Fatalf("pass2 decode: %v", err)
			} else if n, ok := v.(*osmpbf.Node); ok {
				if need[n.ID] {
					waysLocs[n.ID] = [2]float64{n.Lat, n.Lon}
				}
			}
		}
		f2.Close()
	}

	f, err := os.Open(*inPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	d := osmpbf.NewDecoder(f)
	if err := d.Start(runtime.GOMAXPROCS(-1)); err != nil {
		log.Fatalf("decode start: %v", err)
	}
	var out []station
	for {
		v, err := d.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("decode: %v", err)
		}
		switch o := v.(type) {
		case *osmpbf.Node:
			if tagFilter(o.Tags) {
				var lon, lat float64
				lon, lat = o.Lon, o.Lat
				out = append(out, station{ID: o.ID, Kind: "node", Name: o.Tags["name"], Tags: keepTags(o.Tags), Lat: lat, Lon: lon})
			}
		case *osmpbf.Way:
			if tagFilter(o.Tags) {
				var lat, lon float64
				var cnt int
				for _, n := range o.NodeIDs {
					if p, ok := waysLocs[n]; ok {
						lat += p[0]
						lon += p[1]
						cnt++
					}
				}
				if cnt > 0 {
					out = append(out, station{ID: o.ID, Kind: "way", Name: o.Tags["name"], Tags: keepTags(o.Tags), Lat: lat / float64(cnt), Lon: lon / float64(cnt)})
				}
			}
		}
	}

	w, err := os.Create(*outPath)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("stations=%d\n", len(out))
}

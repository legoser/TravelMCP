package geo

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"

	"travelmcp/internal/model"
)

//go:embed places.json
var placesJSON []byte

type Place struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Lat     float64  `json:"lat"`
	Lon     float64  `json:"lon"`
}

type Gazetteer struct {
	places []Place
}

func DefaultGazetteer() (*Gazetteer, error) {
	var places []Place
	if err := json.Unmarshal(placesJSON, &places); err != nil {
		return nil, err
	}
	return &Gazetteer{places: places}, nil
}

func (g *Gazetteer) Add(name string, lat, lon float64) {
	p := Place{Name: name, Lat: lat, Lon: lon}
	if i := strings.Index(name, ","); i >= 0 {
		p.Aliases = []string{strings.TrimSpace(name[:i])}
	}
	g.places = append(g.places, p)
}

func (g *Gazetteer) AddStops(stops []*model.Stop) {
	sorted := make([]*model.Stop, len(stops))
	copy(sorted, stops)
	sort.Slice(sorted, func(i, j int) bool {
		if len(sorted[i].Name) != len(sorted[j].Name) {
			return len(sorted[i].Name) < len(sorted[j].Name)
		}
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].ID < sorted[j].ID
	})
	seen := make(map[string]bool)
	for _, s := range sorted {
		alias := normQuery(s.Name)
		if i := strings.Index(s.Name, ","); i >= 0 {
			alias = normQuery(s.Name[:i])
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		g.Add(s.Name, s.Lat, s.Lon)
	}
}

func normQuery(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}

func (g *Gazetteer) Resolve(query string) (model.Coords, bool) {
	r := g.ResolveDetailed(query)
	return r.Coords, r.Found
}

// Len — размер справочника (диагностика наполнения в логах).
func (g *Gazetteer) Len() int { return len(g.places) }

// exactMatch checks if the stop name contains the query as a whole word/token,
// not as a substring of a longer word. This prevents "омск" from matching "томск".
func exactMatch(stopName, query string) bool {
	ln := strings.ToLower(stopName)
	q := strings.ToLower(query)
	if ln == q {
		return true
	}
	qTokens := strings.Fields(q)
	for _, tok := range qTokens {
		if ln == tok || strings.HasSuffix(ln, " "+tok) || strings.HasPrefix(ln, tok+" ") || strings.Contains(ln, " "+tok+" ") {
			return true
		}
	}
	return false
}

package network

import (
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

type fakeSource struct {
	name  string
	stops []domain.Stop
	rts   []domain.Route
	fps   []domain.Footpath
}

func (f *fakeSource) Name() string                 { return f.name }
func (f *fakeSource) Source() string               { return "test" }
func (f *fakeSource) Version() string              { return "v1" }
func (f *fakeSource) Stops() []domain.Stop         { return f.stops }
func (f *fakeSource) Routes() []domain.Route       { return f.rts }
func (f *fakeSource) Footpaths() []domain.Footpath { return f.fps }

func TestBuildRebasesAndIndexes(t *testing.T) {
	a := &fakeSource{
		name: "a",
		stops: []domain.Stop{
			{ID: 0, Name: "A0", Loc: domain.LatLng{Lat: 12.9700, Lng: 77.5900}},
			{ID: 1, Name: "A1", Loc: domain.LatLng{Lat: 12.9800, Lng: 77.6000}},
		},
		rts: []domain.Route{{ID: 0, ShortName: "R-A", Stops: []domain.StopID{0, 1}, Trips: []domain.Trip{{}}}},
	}
	b := &fakeSource{
		name: "b",
		stops: []domain.Stop{
			{ID: 0, Name: "B0", Loc: domain.LatLng{Lat: 12.9701, Lng: 77.5901}}, // ~15 m from A0
			{ID: 1, Name: "B1", Loc: domain.LatLng{Lat: 13.0000, Lng: 77.7000}},
		},
		rts: []domain.Route{{ID: 0, ShortName: "R-B", Stops: []domain.StopID{0, 1}}},
		fps: []domain.Footpath{{From: 0, To: 1, Seconds: 600}}, // explicit long transfer
	}
	n, err := Build([]Source{a, b}, BuildOptions{FootpathMaxM: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Stops) != 4 || len(n.Routes) != 2 {
		t.Fatalf("stops %d routes %d", len(n.Stops), len(n.Routes))
	}
	if n.Routes[1].Stops[0] != 2 || n.Routes[1].Stops[1] != 3 || n.Routes[1].ID != 1 {
		t.Fatalf("route b not rebased: %+v", n.Routes[1])
	}
	if got := n.StopRoutes[2]; len(got) != 1 || got[0].Route != 1 || got[0].Pos != 0 {
		t.Fatalf("StopRoutes[2] = %+v", got)
	}
	// Geometric footpath A0<->B0 both ways, plus explicit B0->B1.
	if fp := n.FootpathsFrom(0); len(fp) != 1 || fp[0].To != 2 {
		t.Fatalf("footpaths from A0 = %+v", fp)
	}
	fpB0 := n.FootpathsFrom(2)
	if len(fpB0) != 2 {
		t.Fatalf("footpaths from B0 = %+v", fpB0)
	}
	var sawExplicit bool
	for _, f := range fpB0 {
		if f.To == 3 && f.Seconds == 600 {
			sawExplicit = true
		}
	}
	if !sawExplicit {
		t.Fatalf("explicit footpath missing: %+v", fpB0)
	}
	if len(n.Datasets) != 2 || n.Datasets[0].Trips != 1 || n.Version != "a@v1+b@v1" {
		t.Fatalf("datasets %+v version %q", n.Datasets, n.Version)
	}
}

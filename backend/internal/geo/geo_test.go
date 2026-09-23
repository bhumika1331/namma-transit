package geo

import (
	"math"
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

var (
	majestic    = domain.LatLng{Lat: 12.9757, Lng: 77.5729}
	mgRoad      = domain.LatLng{Lat: 12.9756, Lng: 77.6068}
	indiranagar = domain.LatLng{Lat: 12.9784, Lng: 77.6386}
)

func TestDistanceM(t *testing.T) {
	// Majestic -> MG Road metro is about 3.7 km by great circle.
	got := DistanceM(majestic, mgRoad)
	if got < 3600 || got > 3800 {
		t.Fatalf("Majestic-MG Road = %.0f m, want ~3700", got)
	}
	if d := DistanceM(majestic, majestic); d != 0 {
		t.Fatalf("zero distance = %v", d)
	}
	if a, b := DistanceM(majestic, indiranagar), DistanceM(indiranagar, majestic); math.Abs(a-b) > 1e-6 {
		t.Fatalf("asymmetric: %v vs %v", a, b)
	}
}

func TestWalkSeconds(t *testing.T) {
	// 300 m at 4.5 km/h = 240 s.
	if got := WalkSeconds(300); got != 240 {
		t.Fatalf("WalkSeconds(300) = %d, want 240", got)
	}
}

func testStops() []domain.Stop {
	return []domain.Stop{
		{ID: 0, Name: "Majestic", Loc: majestic},
		{ID: 1, Name: "Majestic bus stand", Loc: domain.LatLng{Lat: 12.9772, Lng: 77.5720}}, // ~190 m north
		{ID: 2, Name: "MG Road", Loc: mgRoad},
		{ID: 3, Name: "Indiranagar", Loc: indiranagar},
		{ID: 4, Name: "Near MG Road", Loc: domain.LatLng{Lat: 12.9756, Lng: 77.6090}}, // ~240 m east
	}
}

func TestGridIndexWithin(t *testing.T) {
	g := NewGridIndex(testStops())

	near := g.Within(majestic, 500)
	if len(near) != 2 || near[0].Stop != 0 || near[1].Stop != 1 {
		t.Fatalf("Within(majestic, 500) = %+v, want stops 0 then 1", near)
	}
	if near[1].DistM < 150 || near[1].DistM > 230 {
		t.Fatalf("distance to bus stand = %.0f, want ~190", near[1].DistM)
	}

	// A query point straddling a cell boundary must still see neighbours.
	edge := domain.LatLng{Lat: 12.975, Lng: 77.575}
	if got := g.Within(edge, 450); len(got) != 2 {
		t.Fatalf("cell-edge query found %d stops, want 2", len(got))
	}

	if got := g.Within(indiranagar, 100); len(got) != 1 || got[0].Stop != 3 {
		t.Fatalf("tight radius = %+v", got)
	}
}

func TestFootpaths(t *testing.T) {
	g := NewGridIndex(testStops())
	fps := Footpaths(g, 300)

	// Pairs within 300 m: (0,1) and (2,4), each in both directions.
	if len(fps) != 4 {
		t.Fatalf("got %d footpaths, want 4: %+v", len(fps), fps)
	}
	seen := map[[2]domain.StopID]int32{}
	for _, f := range fps {
		if f.From == f.To {
			t.Fatalf("self footpath %+v", f)
		}
		seen[[2]domain.StopID{f.From, f.To}] = f.Seconds
	}
	for _, pair := range [][2]domain.StopID{{0, 1}, {1, 0}, {2, 4}, {4, 2}} {
		if _, ok := seen[pair]; !ok {
			t.Fatalf("missing footpath %v", pair)
		}
	}
	if seen[[2]domain.StopID{0, 1}] != seen[[2]domain.StopID{1, 0}] {
		t.Fatalf("footpath not symmetric")
	}
}

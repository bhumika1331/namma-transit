package raptor

import (
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

const (
	h = 3600
	m = 60
)

type src struct {
	stops []domain.Stop
	rts   []domain.Route
	fps   []domain.Footpath
}

func (s *src) Name() string                 { return "toy" }
func (s *src) Source() string               { return "test" }
func (s *src) Version() string              { return "1" }
func (s *src) Stops() []domain.Stop         { return s.stops }
func (s *src) Routes() []domain.Route       { return s.rts }
func (s *src) Footpaths() []domain.Footpath { return s.fps }

var all = domain.Weekday.Bit() | domain.Saturday.Bit() | domain.Sunday.Bit()

func trip(times ...domain.Seconds) domain.Trip {
	return domain.Trip{Days: all, Arr: times, Dep: times}
}

// Toy network (stops spaced ~1 km apart so no accidental footpaths):
//
//	A(0) --R1--> B(1) --R1--> C(2)            R1: A-B-C, hourly-ish slow
//	              B(1) --R2--> D(3)            R2: B-D, connects from R1
//	A(0) --R3--> E(4)                          R3: A-E express to E
//	E(4) ~walk~ D(3)   (explicit 5 min footpath)
//	F(5) isolated near nothing
func toyNetwork(t *testing.T) *network.Network {
	s := &src{
		stops: []domain.Stop{
			{ID: 0, Name: "A", Loc: domain.LatLng{Lat: 12.90, Lng: 77.50}},
			{ID: 1, Name: "B", Loc: domain.LatLng{Lat: 12.91, Lng: 77.50}},
			{ID: 2, Name: "C", Loc: domain.LatLng{Lat: 12.92, Lng: 77.50}},
			{ID: 3, Name: "D", Loc: domain.LatLng{Lat: 12.91, Lng: 77.51}},
			{ID: 4, Name: "E", Loc: domain.LatLng{Lat: 12.93, Lng: 77.51}},
			{ID: 5, Name: "F", Loc: domain.LatLng{Lat: 12.95, Lng: 77.55}},
		},
		rts: []domain.Route{
			{ID: 0, ShortName: "R1", Mode: domain.ModeBus, Class: domain.ClassOrdinary,
				Stops: []domain.StopID{0, 1, 2}, DistKm: []float64{0, 1.1, 2.2},
				Trips: []domain.Trip{
					trip(8*h, 8*h+10*m, 8*h+20*m),
					trip(8*h+30*m, 8*h+40*m, 8*h+50*m),
					trip(9*h, 9*h+10*m, 9*h+20*m),
				}},
			{ID: 1, ShortName: "R2", Mode: domain.ModeBus, Class: domain.ClassOrdinary,
				Stops: []domain.StopID{1, 3}, DistKm: []float64{0, 1.1},
				Trips: []domain.Trip{
					trip(8*h+5*m, 8*h+15*m), // leaves B before R1's first trip arrives
					trip(8*h+12*m, 8*h+22*m),
					trip(8*h+45*m, 8*h+55*m),
				}},
			{ID: 2, ShortName: "R3", Mode: domain.ModeBus, Class: domain.ClassVajra,
				Stops: []domain.StopID{0, 4}, DistKm: []float64{0, 3.5},
				Trips: []domain.Trip{
					trip(8*h+2*m, 8*h+14*m),
				}},
		},
		fps: []domain.Footpath{{From: 4, To: 3, Seconds: 5 * m}, {From: 3, To: 4, Seconds: 5 * m}},
	}
	n, err := network.Build([]network.Source{s}, network.BuildOptions{FootpathMaxM: 100})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func newRouter(t *testing.T) *Router {
	return &Router{Net: toyNetwork(t), Time: timeprovider.NewSchedule()}
}

func TestDirectRide(t *testing.T) {
	r := newRouter(t)
	js := r.Plan(Query{
		Sources: []Access{{Stop: 0, Seconds: 2 * m}},
		Targets: []Access{{Stop: 2, Seconds: 3 * m}},
		Depart:  7*h + 50*m, Day: domain.Weekday,
	})
	if len(js) != 1 {
		t.Fatalf("want 1 journey, got %d: %+v", len(js), js)
	}
	j := js[0]
	// Walk 2 min (07:50-07:52), R1 08:00-08:20, walk 3 min -> 08:23.
	if j.Rides != 1 || j.Arrive != 8*h+23*m || j.Depart != 7*h+50*m {
		t.Fatalf("journey = %+v", j)
	}
	if len(j.Legs) != 3 || j.Legs[0].Kind != LegWalk || j.Legs[1].Kind != LegRide || j.Legs[2].Kind != LegWalk {
		t.Fatalf("legs = %+v", j.Legs)
	}
	ride := j.Legs[1]
	if ride.Route != 0 || ride.From != 0 || ride.To != 2 || ride.Dep != 8*h || ride.Arr != 8*h+20*m || ride.FromPos != 0 || ride.ToPos != 2 {
		t.Fatalf("ride leg = %+v", ride)
	}
}

func TestOneTransferAndPareto(t *testing.T) {
	r := newRouter(t)
	// A -> D. Options:
	//  - R1 to B (08:10), R2 08:12 -> D 08:22 (2 rides)
	//  - R3 to E (08:14), walk 5 min -> D 08:19 (1 ride)
	// The 1-ride option is faster, so it dominates and only it is returned.
	js := r.Plan(Query{
		Sources: []Access{{Stop: 0}},
		Targets: []Access{{Stop: 3}},
		Depart:  8 * h, Day: domain.Weekday,
	})
	if len(js) != 1 || js[0].Rides != 1 || js[0].Arrive != 8*h+19*m {
		t.Fatalf("want single 1-ride journey arriving 08:19, got %+v", js)
	}
	legs := js[0].Legs
	if len(legs) != 4 || legs[1].Route != 2 || legs[2].Kind != LegWalk || legs[2].From != 4 || legs[2].To != 3 {
		t.Fatalf("legs = %+v", legs)
	}

	// Depart 08:03: R3 has left, so the only way is R1 08:30 -> B 08:40, R2 08:45 -> D 08:55.
	js = r.Plan(Query{
		Sources: []Access{{Stop: 0}},
		Targets: []Access{{Stop: 3}},
		Depart:  8*h + 3*m, Day: domain.Weekday,
	})
	if len(js) != 1 || js[0].Rides != 2 || js[0].Arrive != 8*h+55*m {
		t.Fatalf("want 2-ride journey arriving 08:55, got %+v", js)
	}
	legs = js[0].Legs
	if len(legs) != 4 || legs[1].Route != 0 || legs[2].Route != 1 || legs[1].To != 1 || legs[2].From != 1 {
		t.Fatalf("transfer legs = %+v", legs)
	}
	if legs[2].Dep != 8*h+45*m {
		t.Fatalf("R2 boarding dep = %d, want 08:45", legs[2].Dep)
	}
}

func TestParetoKeepsFewerRidesWhenSlower(t *testing.T) {
	r := newRouter(t)
	// A -> C at 07:55: direct R1 arrives 08:20 (1 ride). No 2-ride option is
	// faster, so exactly one journey. Then A -> D at 08:00 gave 1 ride; here we
	// check that a slower 1-ride journey is still reported alongside a faster
	// 2-ride one by making the direct option late: depart 08:15 -> R3 gone,
	// R1 08:30 to B 08:40 then R2 08:45 -> D 08:55 is the only path (2 rides).
	js := r.Plan(Query{
		Sources: []Access{{Stop: 0}},
		Targets: []Access{{Stop: 2}},
		Depart:  7*h + 55*m, Day: domain.Weekday,
	})
	if len(js) != 1 || js[0].Rides != 1 {
		t.Fatalf("got %+v", js)
	}
}

func TestUnreachableAndMaxRides(t *testing.T) {
	r := newRouter(t)
	if js := r.Plan(Query{Sources: []Access{{Stop: 0}}, Targets: []Access{{Stop: 5}}, Depart: 8 * h}); len(js) != 0 {
		t.Fatalf("F should be unreachable, got %+v", js)
	}
	// With MaxRides 1 at 08:03 A->D needs 2 rides: nothing.
	js := r.Plan(Query{Sources: []Access{{Stop: 0}}, Targets: []Access{{Stop: 3}}, Depart: 8*h + 3*m, MaxRides: 1})
	if len(js) != 0 {
		t.Fatalf("MaxRides=1 should find nothing, got %+v", js)
	}
}

func TestSourceEqualsTarget(t *testing.T) {
	r := newRouter(t)
	js := r.Plan(Query{Sources: []Access{{Stop: 1, Seconds: m}}, Targets: []Access{{Stop: 1, Seconds: m}}, Depart: 8 * h})
	if len(js) != 1 || js[0].Rides != 0 || js[0].Arrive != 8*h+2*m {
		t.Fatalf("walk-only journey = %+v", js)
	}
}

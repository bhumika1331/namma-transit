package planner

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
	"github.com/bhumika1331/namma-transit/backend/internal/fare"
	"github.com/bhumika1331/namma-transit/backend/internal/gtfs"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
	"github.com/bhumika1331/namma-transit/backend/internal/places"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

func realPlanner(t *testing.T, feeds ...string) *Planner {
	var srcs []network.Source
	for _, name := range feeds {
		path := "../../data/gtfs/" + name + ".zip"
		if _, err := os.Stat(path); err != nil {
			t.Skipf("feed missing: %v", err)
		}
		ds, err := gtfs.Load(path, gtfs.Options{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, ds)
	}
	n, err := network.Build(srcs, network.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tables, err := fare.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	return New(n, timeprovider.NewSchedule(), &fare.Engine{Tables: tables}, places.New(n, nil))
}

func placeReq(from, to string, at time.Time, total, women int32) *transitv1.PlanTripRequest {
	return &transitv1.PlanTripRequest{
		Origin:      &transitv1.Location{Ref: &transitv1.Location_PlaceId{PlaceId: from}},
		Destination: &transitv1.Location{Ref: &transitv1.Location_PlaceId{PlaceId: to}},
		Departure:   timestamppb.New(at),
		Party:       &transitv1.Party{Total: total, Women: women},
	}
}

var thu10 = time.Date(2026, 9, 24, 10, 0, 0, 0, IST)

func kinds(resp *transitv1.PlanTripResponse) map[transitv1.ItineraryKind]*transitv1.Itinerary {
	out := map[transitv1.ItineraryKind]*transitv1.Itinerary{}
	for _, it := range resp.Itineraries {
		if _, ok := out[it.Kind]; !ok {
			out[it.Kind] = it
		}
	}
	return out
}

func TestMetroOnlyMajesticIndiranagar(t *testing.T) {
	p := realPlanner(t, "bmrcl")
	resp, err := p.Plan(context.Background(), placeReq("metro:PURPLE:KGWA", "metro:PURPLE:IDN", thu10, 5, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Itineraries) != 1 {
		t.Fatalf("want exactly one metro itinerary, got %d", len(resp.Itineraries))
	}
	it := resp.Itineraries[0]
	if it.Kind != transitv1.ItineraryKind_ITINERARY_KIND_METRO_ONLY || it.Transfers != 0 {
		t.Fatalf("kind %v transfers %d", it.Kind, it.Transfers)
	}
	if it.TotalMin < 12 || it.TotalMin > 20 {
		t.Errorf("total %d min", it.TotalMin)
	}
	// ~8.6 km -> ₹50 slab; metro charges women too: 5 x 50.
	if it.Cost.GroupTotal.Paise != 5*5000 || it.Cost.PerPersonWoman.Paise != 5000 {
		t.Errorf("cost %+v", it.Cost)
	}
	ride := it.Legs[1]
	if ride.Mode != transitv1.LegMode_LEG_MODE_METRO_RAIL || ride.RouteShortName != "Purple Line" || len(ride.IntermediateStops) != 6 || ride.LineColor == "" {
		t.Errorf("ride leg %+v", ride)
	}
	if resp.Auto == nil || resp.Auto.Autos != 2 || resp.Auto.GroupTotal.Paise <= 0 {
		t.Errorf("auto %+v", resp.Auto)
	}
	if resp.DataVersion == "" {
		t.Error("no data version")
	}
}

func TestBusAndMetroMajesticIndiranagar(t *testing.T) {
	p := realPlanner(t, "bmrcl", "bmtc")
	resp, err := p.Plan(context.Background(), placeReq("metro:PURPLE:KGWA", "metro:PURPLE:IDN", thu10, 5, 3))
	if err != nil {
		t.Fatal(err)
	}
	k := kinds(resp)
	metro, bus := k[transitv1.ItineraryKind_ITINERARY_KIND_METRO_ONLY], k[transitv1.ItineraryKind_ITINERARY_KIND_DIRECT_BUS]
	if metro == nil || bus == nil {
		t.Fatalf("want metro and direct bus, got kinds %v", keys(k))
	}
	// Direct ordinary bus: only the 2 non-women pay, so the group pays less than metro.
	if bus.Cost.GroupTotal.Paise >= metro.Cost.GroupTotal.Paise {
		t.Errorf("bus %d should be cheaper than metro %d for 5/3", bus.Cost.GroupTotal.Paise, metro.Cost.GroupTotal.Paise)
	}
	if bus.Cost.PerPersonWoman.Paise != 0 {
		t.Errorf("women should ride the ordinary bus free: %+v", bus.Cost)
	}
	if metro.TotalMin >= bus.TotalMin {
		t.Errorf("metro %d min should beat bus %d min", metro.TotalMin, bus.TotalMin)
	}
	for _, it := range resp.Itineraries {
		for _, l := range it.Legs {
			if l.ServiceClass == transitv1.ServiceClass_SERVICE_CLASS_VAYU_VAJRA {
				t.Errorf("Vayu Vajra should be excluded: %s", l.RouteShortName)
			}
		}
	}
}

func TestKoramangalaWhitefield(t *testing.T) {
	p := realPlanner(t, "bmrcl", "bmtc")
	start := time.Now()
	resp, err := p.Plan(context.Background(), placeReq("bus:35267", "metro:PURPLE:WHTM", thu10.Add(-time.Hour), 4, 2))
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("planning took %s", d)
	}
	k := kinds(resp)
	if k[transitv1.ItineraryKind_ITINERARY_KIND_BUS_METRO] == nil {
		t.Errorf("want a bus+metro option, got %v", keys(k))
	}
	if k[transitv1.ItineraryKind_ITINERARY_KIND_BUS_BUS] == nil && k[transitv1.ItineraryKind_ITINERARY_KIND_DIRECT_BUS] == nil {
		t.Errorf("want a bus-only option, got %v", keys(k))
	}
	var fastest, cheapest int
	for _, it := range resp.Itineraries {
		for _, tag := range it.Tags {
			switch tag {
			case "fastest":
				fastest++
			case "cheapest":
				cheapest++
			}
		}
	}
	if fastest != 1 || cheapest != 1 {
		t.Errorf("tags: fastest %d cheapest %d", fastest, cheapest)
	}
}

func TestUnknownPlaceAndNoStops(t *testing.T) {
	p := realPlanner(t, "bmrcl")
	if _, err := p.Plan(context.Background(), placeReq("metro:NOPE", "metro:PURPLE:IDN", thu10, 1, 0)); err == nil {
		t.Error("unknown place should error")
	}
	// Middle of nowhere (Arabian Sea): no stops, warning, auto still computed.
	req := placeReq("metro:PURPLE:KGWA", "", thu10, 2, 0)
	req.Destination = &transitv1.Location{Ref: &transitv1.Location_LatLng{LatLng: &transitv1.LatLng{Lat: 12.9, Lng: 74.0}}}
	resp, err := p.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Itineraries) != 0 || len(resp.Warnings) == 0 || resp.Auto == nil {
		t.Errorf("resp %+v", resp)
	}
}

func keys(m map[transitv1.ItineraryKind]*transitv1.Itinerary) []string {
	var out []string
	for k := range m {
		out = append(out, k.String())
	}
	return out
}

func TestMergeWalks(t *testing.T) {
	w := func(min int32) *transitv1.Leg {
		return &transitv1.Leg{Mode: transitv1.LegMode_LEG_MODE_WALK, DurationMin: min, DistanceKm: 0.3}
	}
	ride := &transitv1.Leg{Mode: transitv1.LegMode_LEG_MODE_BUS}
	got := mergeWalks([]*transitv1.Leg{w(7), w(4), ride, w(1), w(0)})
	if len(got) != 3 || got[0].DurationMin != 11 || got[0].DistanceKm != 0.6 || got[2].DurationMin != 1 {
		t.Fatalf("merged = %+v", got)
	}
}

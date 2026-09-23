// Package planner turns a PlanTrip request into priced, ranked itineraries:
// resolve places to nearby stops, run RAPTOR at a few departure times, price
// each journey, classify it, and rank by time and group cost.
package planner

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/fare"
	"github.com/bhumika1331/namma-transit/backend/internal/geo"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
	"github.com/bhumika1331/namma-transit/backend/internal/places"
	"github.com/bhumika1331/namma-transit/backend/internal/routing/raptor"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

// IST is the only time zone the network runs in.
var IST = time.FixedZone("IST", 5*3600+1800)

// Planner is safe for concurrent use.
type Planner struct {
	Net    *network.Network
	Time   *timeprovider.Schedule
	Fare   *fare.Engine
	Places *places.Index
	Now    func() time.Time

	// AccessRadiusM and MaxAccessRadiusM bound the walk to the first stop.
	AccessRadiusM, MaxAccessRadiusM float64
	// Departures offsets (in addition to the requested time) that widen the
	// result set so a slower direct bus still shows next to a faster metro.
	Departures []domain.Seconds
	// ExcludeClasses are not routed. Vayu Vajra airport buses are excluded
	// until their route-fixed fares are scraped; pricing them on the Vajra
	// chart would understate the cost badly.
	ExcludeClasses []domain.ServiceClass
}

// New wires a planner with defaults.
func New(n *network.Network, tp *timeprovider.Schedule, fe *fare.Engine, pl *places.Index) *Planner {
	return &Planner{
		Net: n, Time: tp, Fare: fe, Places: pl,
		Now:              time.Now,
		AccessRadiusM:    500,
		MaxAccessRadiusM: 1000,
		Departures:       []domain.Seconds{0, 10 * 60, 20 * 60},
		ExcludeClasses:   []domain.ServiceClass{domain.ClassVayuVajra},
	}
}

// Plan implements the PlanTrip RPC.
func (p *Planner) Plan(ctx context.Context, req *transitv1.PlanTripRequest) (*transitv1.PlanTripResponse, error) {
	resp := &transitv1.PlanTripResponse{DataVersion: p.Net.Version}

	origin, err := p.resolve(req.GetOrigin())
	if err != nil {
		return nil, fmt.Errorf("origin: %w", err)
	}
	dest, err := p.resolve(req.GetDestination())
	if err != nil {
		return nil, fmt.Errorf("destination: %w", err)
	}

	depart := p.Now().In(IST)
	if ts := req.GetDeparture(); ts != nil && ts.IsValid() {
		depart = ts.AsTime().In(IST)
	}
	dayStart := time.Date(depart.Year(), depart.Month(), depart.Day(), 0, 0, 0, 0, IST)
	t0 := domain.Seconds(depart.Sub(dayStart) / time.Second)
	day := domain.DayKindOf(depart)

	party := fare.Party{
		Total:      int(req.GetParty().GetTotal()),
		Women:      int(req.GetParty().GetWomen()),
		SmartCards: int(req.GetParty().GetSmartCardHolders()),
	}
	if party.Total <= 0 {
		party.Total = 1
	}
	if party.Women > party.Total {
		party.Women = party.Total
	}

	sources, warn := p.access(origin, "origin")
	resp.Warnings = append(resp.Warnings, warn...)
	targets, warn := p.access(dest, "destination")
	resp.Warnings = append(resp.Warnings, warn...)

	straightKm := geo.DistanceM(origin, dest) / 1000
	hour := depart.Hour()
	resp.Auto = p.auto(straightKm, party.Total, hour >= 22 || hour < 5, p.Time.Profile.IsPeak(t0, day))

	if len(sources) == 0 || len(targets) == 0 {
		return resp, nil
	}
	if straightKm < 0.4 {
		resp.Warnings = append(resp.Warnings, "Origin and destination are within walking distance.")
	}

	router := &raptor.Router{Net: p.Net, Time: p.Time}
	seen := map[string]bool{}
	var its []*transitv1.Itinerary
	// Three passes: everything, bus-only, metro-only. Pareto pruning inside
	// RAPTOR would otherwise hide the slower mode, and the whole point is to
	// compare them on cost.
	passes := [][]domain.Mode{nil, {domain.ModeBus}, {domain.ModeMetroRail}}
	for _, modes := range passes {
		for _, off := range p.Departures {
			js := router.Plan(raptor.Query{Sources: sources, Targets: targets, Depart: t0 + off, Day: day, MaxRides: 2, Modes: modes, ExcludeClasses: p.ExcludeClasses})
			for _, j := range js {
				if j.Rides == 0 {
					continue
				}
				key := journeyKey(j)
				if seen[key] {
					continue
				}
				seen[key] = true
				its = append(its, p.itinerary(j, dayStart, day, party, origin, dest))
			}
		}
	}
	if len(its) == 0 {
		resp.Warnings = append(resp.Warnings, "No bus or metro connection found for that time.")
		return resp, nil
	}
	resp.Itineraries = rank(its, party.Total, int(req.GetMaxItineraries()))
	return resp, nil
}

func (p *Planner) resolve(loc *transitv1.Location) (domain.LatLng, error) {
	switch ref := loc.GetRef().(type) {
	case *transitv1.Location_PlaceId:
		if pt, ok := places.ParseGeoPlaceID(ref.PlaceId); ok {
			return pt, nil
		}
		id, ok := p.Places.Lookup(ref.PlaceId)
		if !ok {
			return domain.LatLng{}, fmt.Errorf("unknown place %q", ref.PlaceId)
		}
		return p.Net.Stops[id].Loc, nil
	case *transitv1.Location_LatLng:
		if ref.LatLng == nil || (ref.LatLng.Lat == 0 && ref.LatLng.Lng == 0) {
			return domain.LatLng{}, fmt.Errorf("empty coordinates")
		}
		return domain.LatLng{Lat: ref.LatLng.Lat, Lng: ref.LatLng.Lng}, nil
	}
	return domain.LatLng{}, fmt.Errorf("location required")
}

// access finds stops walkable from a point, widening the radius once.
func (p *Planner) access(pt domain.LatLng, label string) ([]raptor.Access, []string) {
	near := p.Net.Grid.Within(pt, p.AccessRadiusM)
	var warn []string
	if len(near) == 0 {
		near = p.Net.Grid.Within(pt, p.MaxAccessRadiusM)
		if len(near) == 0 {
			return nil, []string{fmt.Sprintf("No bus stop or metro station within %.0f m of the %s.", p.MaxAccessRadiusM, label)}
		}
		warn = append(warn, fmt.Sprintf("Nearest stop to the %s is %.0f m away.", label, near[0].DistM))
	}
	out := make([]raptor.Access, len(near))
	for i, n := range near {
		out[i] = raptor.Access{Stop: n.Stop, Seconds: domain.Seconds(geo.WalkSeconds(n.DistM))}
	}
	return out, warn
}

func journeyKey(j raptor.Journey) string {
	var b strings.Builder
	for _, l := range j.Legs {
		if l.Kind == raptor.LegRide {
			fmt.Fprintf(&b, "%d:%d-%d;", l.Route, l.FromPos, l.ToPos)
		}
	}
	return b.String()
}

func (p *Planner) itinerary(j raptor.Journey, dayStart time.Time, day domain.DayKind, party fare.Party, origin, dest domain.LatLng) *transitv1.Itinerary {
	ts := func(s domain.Seconds) *timestamppb.Timestamp {
		return timestampAt(dayStart, s)
	}
	it := &transitv1.Itinerary{
		Depart:    ts(j.Depart),
		Arrive:    ts(j.Arrive),
		TotalMin:  (j.Arrive - j.Depart).Minutes(),
		Transfers: int32(j.Rides - 1),
	}

	// Pricing: consecutive metro rides are one ticket, so merge them.
	var fareLegs []fare.Leg
	var fareLegForProto []int // index into fareLegs per ride leg, -1 for "included in previous"
	var walk domain.Seconds
	var buses, metros int
	lastWasMetro := false
	for _, l := range j.Legs {
		if l.Kind == raptor.LegWalk {
			walk += l.Arr - l.Dep
			continue
		}
		route := &p.Net.Routes[l.Route]
		km := route.DistKm[l.ToPos] - route.DistKm[l.FromPos]
		if route.Mode == domain.ModeMetroRail {
			metros++
			if lastWasMetro {
				fareLegs[len(fareLegs)-1].Km += km
				fareLegForProto = append(fareLegForProto, -1)
				continue
			}
			lastWasMetro = true
		} else {
			buses++
			lastWasMetro = false
		}
		fareLegs = append(fareLegs, fare.Leg{Class: route.Class, Km: km, Peak: p.Time.Profile.IsPeak(l.Dep, day)})
		fareLegForProto = append(fareLegForProto, len(fareLegs)-1)
	}
	it.WalkMin = walk.Minutes()
	total := p.Fare.Price(fareLegs, party)

	ride := 0
	for _, l := range j.Legs {
		leg := &transitv1.Leg{Depart: ts(l.Dep), Arrive: ts(l.Arr), DurationMin: (l.Arr - l.Dep).Minutes()}
		if l.Kind == raptor.LegWalk {
			leg.Mode = transitv1.LegMode_LEG_MODE_WALK
			leg.From = p.placeOrPoint(l.From, origin, l.From == l.To && ride == 0)
			leg.To = p.placeOrPoint(l.To, dest, l.From == l.To && ride > 0)
			leg.DistanceKm = math.Round(float64(l.Arr-l.Dep)*geo.WalkSpeedMPS/100) / 10
			it.Legs = append(it.Legs, leg)
			continue
		}
		route := &p.Net.Routes[l.Route]
		leg.RouteShortName = route.ShortName
		leg.Headsign = route.Headsign
		leg.LineColor = route.LineColor
		leg.ServiceClass = toProtoClass(route.Class)
		leg.From = places.ToPlace(p.Net.Stops[l.From])
		leg.To = places.ToPlace(p.Net.Stops[l.To])
		leg.DistanceKm = math.Round((route.DistKm[l.ToPos]-route.DistKm[l.FromPos])*10) / 10
		for pos := l.FromPos + 1; pos < l.ToPos; pos++ {
			leg.IntermediateStops = append(leg.IntermediateStops, places.ToPlace(p.Net.Stops[route.Stops[pos]]))
		}
		if route.Mode == domain.ModeMetroRail {
			leg.Mode = transitv1.LegMode_LEG_MODE_METRO_RAIL
		} else {
			leg.Mode = transitv1.LegMode_LEG_MODE_BUS
		}
		if l.Boarding.Scheduled() {
			leg.HeadwayMin = timeprovider.HeadwayAround(route, l.FromPos, l.Dep, day).Minutes()
		} else {
			leg.HeadwayMin = l.Boarding.Headway.Minutes()
		}
		if fi := fareLegForProto[ride]; fi >= 0 {
			lp := total.Legs[fi]
			leg.FarePerPerson = &transitv1.Money{Paise: lp.PerPerson}
			leg.StageCount = int32(lp.Stages)
			leg.WomenFree = lp.WomenFree
		} else {
			leg.FarePerPerson = &transitv1.Money{Paise: 0}
		}
		ride++
		it.Legs = append(it.Legs, leg)
	}

	it.Legs = mergeWalks(it.Legs)
	it.Cost = toCost(total, party)
	switch {
	case buses == 0:
		it.Kind = transitv1.ItineraryKind_ITINERARY_KIND_METRO_ONLY
	case metros == 0 && buses == 1:
		it.Kind = transitv1.ItineraryKind_ITINERARY_KIND_DIRECT_BUS
	case metros == 0:
		it.Kind = transitv1.ItineraryKind_ITINERARY_KIND_BUS_BUS
	default:
		it.Kind = transitv1.ItineraryKind_ITINERARY_KIND_BUS_METRO
	}
	for _, fl := range fareLegs {
		if fl.Class == domain.ClassVajra || fl.Class == domain.ClassVayuVajra {
			it.Tags = append(it.Tags, "ac")
			break
		}
	}
	it.Id = fmt.Sprintf("%s|%d", journeyKey(j), j.Depart)
	return it
}

// mergeWalks joins consecutive walking legs (access walk followed by a
// footpath, or a footpath followed by the egress walk) into one.
func mergeWalks(legs []*transitv1.Leg) []*transitv1.Leg {
	out := make([]*transitv1.Leg, 0, len(legs))
	for _, l := range legs {
		if n := len(out); n > 0 && l.Mode == transitv1.LegMode_LEG_MODE_WALK && out[n-1].Mode == transitv1.LegMode_LEG_MODE_WALK {
			prev := out[n-1]
			prev.To = l.To
			prev.Arrive = l.Arrive
			prev.DurationMin += l.DurationMin
			prev.DistanceKm = math.Round((prev.DistanceKm+l.DistanceKm)*10) / 10
			continue
		}
		out = append(out, l)
	}
	return out
}

// placeOrPoint labels access/egress walks with the raw point on one side.
func (p *Planner) placeOrPoint(stop domain.StopID, pt domain.LatLng, usePoint bool) *transitv1.Place {
	if usePoint {
		return &transitv1.Place{Id: "geo", Name: "Your location", Kind: transitv1.PlaceKind_PLACE_KIND_GEOCODED,
			Loc: &transitv1.LatLng{Lat: pt.Lat, Lng: pt.Lng}}
	}
	return places.ToPlace(p.Net.Stops[stop])
}

func (p *Planner) auto(straightKm float64, total int, night, peak bool) *transitv1.AutoBaseline {
	ab := p.Fare.Auto(straightKm, total, night, peak)
	return &transitv1.AutoBaseline{
		Autos:      int32(ab.Autos),
		RoadKm:     math.Round(ab.RoadKm*10) / 10,
		GroupTotal: &transitv1.Money{Paise: ab.Group},
		EstMin:     int32(ab.EstMinutes),
	}
}

func toCost(t fare.Total, party fare.Party) *transitv1.CostBreakdown {
	c := &transitv1.CostBreakdown{
		GroupTotal:      &transitv1.Money{Paise: t.Group},
		PerPersonWoman:  &transitv1.Money{Paise: t.PerWoman},
		PerPersonOther:  &transitv1.Money{Paise: t.PerOther},
		SmartCardSaving: &transitv1.Money{Paise: t.SmartCardSaving},
		DailyPassHint:   t.DailyPassHint,
	}
	if t.DailyPassHint {
		c.DailyPassGroupTotal = &transitv1.Money{Paise: t.DailyPassGroup}
	}
	byClass := map[domain.ServiceClass]*transitv1.ClassCost{}
	var order []domain.ServiceClass
	for _, lp := range t.Legs {
		cc, ok := byClass[lp.Class]
		if !ok {
			cc = &transitv1.ClassCost{ServiceClass: toProtoClass(lp.Class), PayingRiders: int32(lp.PayingRiders),
				PerPerson: &transitv1.Money{}, Group: &transitv1.Money{}}
			byClass[lp.Class] = cc
			order = append(order, lp.Class)
		}
		cc.PerPerson.Paise += lp.PerPerson
		cc.Group.Paise += lp.Group
	}
	for _, k := range order {
		c.ByClass = append(c.ByClass, byClass[k])
	}
	return c
}

func toProtoClass(c domain.ServiceClass) transitv1.ServiceClass {
	switch c {
	case domain.ClassOrdinary:
		return transitv1.ServiceClass_SERVICE_CLASS_ORDINARY
	case domain.ClassVajra:
		return transitv1.ServiceClass_SERVICE_CLASS_VAJRA
	case domain.ClassVayuVajra:
		return transitv1.ServiceClass_SERVICE_CLASS_VAYU_VAJRA
	case domain.ClassMetroFeeder:
		return transitv1.ServiceClass_SERVICE_CLASS_METRO_FEEDER
	case domain.ClassMetro:
		return transitv1.ServiceClass_SERVICE_CLASS_METRO
	}
	return transitv1.ServiceClass_SERVICE_CLASS_UNSPECIFIED
}

func timestampAt(dayStart time.Time, s domain.Seconds) *timestamppb.Timestamp {
	return timestamppb.New(dayStart.Add(time.Duration(s) * time.Second))
}

// rank orders itineraries by minutes plus rupees per head, tags the fastest
// and cheapest, and keeps at least one of every kind present.
func rank(its []*transitv1.Itinerary, total int, limit int) []*transitv1.Itinerary {
	if limit <= 0 {
		limit = 5
	}
	score := func(it *transitv1.Itinerary) float64 {
		return float64(it.TotalMin) + float64(it.Cost.GroupTotal.Paise)/100/float64(total)
	}
	sort.SliceStable(its, func(i, j int) bool { return score(its[i]) < score(its[j]) })

	fastest, cheapest := its[0], its[0]
	for _, it := range its {
		if it.TotalMin < fastest.TotalMin {
			fastest = it
		}
		if it.Cost.GroupTotal.Paise < cheapest.Cost.GroupTotal.Paise {
			cheapest = it
		}
	}
	fastest.Tags = append(fastest.Tags, "fastest")
	if cheapest != fastest {
		cheapest.Tags = append(cheapest.Tags, "cheapest")
	} else {
		fastest.Tags = append(fastest.Tags, "cheapest")
	}

	if len(its) <= limit {
		return its
	}
	kept := its[:limit]
	haveKind := map[transitv1.ItineraryKind]bool{}
	for _, it := range kept {
		haveKind[it.Kind] = true
	}
	for _, it := range its[limit:] {
		if !haveKind[it.Kind] {
			kept = append(kept, it)
			haveKind[it.Kind] = true
		}
	}
	return kept
}

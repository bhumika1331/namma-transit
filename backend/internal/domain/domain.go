// Package domain holds the plain in-memory model of the transit network.
// It has no I/O; loaders (GTFS, SQLite) produce these values and the router,
// fare engine and API consume them.
package domain

import (
	"strings"
	"time"
)

// StopID is a compact index into Network.Stops.
type StopID uint32

// RouteID is a compact index into Network.Routes. One RouteID is one
// direction of one line or bus route (GTFS "route pattern"), so UP and DOWN
// of bus 500D are two RouteIDs.
type RouteID uint32

// Seconds since local midnight (IST). Values past 86400 mean "after midnight
// of the service day", as GTFS does.
type Seconds int32

// Minutes rounds up to whole minutes for display.
func (s Seconds) Minutes() int32 { return int32((s + 59) / 60) }

// Mode distinguishes rail from bus; walking is a footpath, not a route.
type Mode uint8

const (
	ModeBus Mode = iota + 1
	ModeMetroRail
)

// ServiceClass selects the fare chart; mirrors transit.v1.ServiceClass.
type ServiceClass uint8

const (
	ClassUnspecified ServiceClass = iota
	ClassOrdinary
	ClassVajra
	ClassVayuVajra
	ClassMetroFeeder
	ClassMetro
)

// WomenFree reports whether Karnataka's Shakti scheme applies to this class.
func (c ServiceClass) WomenFree() bool {
	return c == ClassOrdinary || c == ClassMetroFeeder
}

func (c ServiceClass) String() string {
	switch c {
	case ClassOrdinary:
		return "ordinary"
	case ClassVajra:
		return "vajra"
	case ClassVayuVajra:
		return "vayu_vajra"
	case ClassMetroFeeder:
		return "metro_feeder"
	case ClassMetro:
		return "metro"
	}
	return "unspecified"
}

// DayKind selects a timetable variant.
type DayKind uint8

const (
	Weekday DayKind = iota
	Saturday
	Sunday
)

// DayMask is a set of DayKinds a trip runs on.
type DayMask uint8

// Bit returns the mask bit for a day kind.
func (d DayKind) Bit() DayMask { return DayMask(1) << d }

// Has reports whether the mask includes the day kind.
func (m DayMask) Has(d DayKind) bool { return m&d.Bit() != 0 }

// DayKindOf maps a local time to its timetable variant.
func DayKindOf(t time.Time) DayKind {
	switch t.Weekday() {
	case time.Saturday:
		return Saturday
	case time.Sunday:
		return Sunday
	}
	return Weekday
}

// LatLng in WGS84 degrees.
type LatLng struct {
	Lat, Lng float64
}

type StopKind uint8

const (
	StopBus StopKind = iota + 1
	StopMetro
)

// Stop is a bus stop or one line's platform of a metro station. A metro
// interchange station appears once per line, joined by a Footpath.
type Stop struct {
	ID       StopID
	SourceID string // id in the upstream dataset ("bus:12345", "metro:PURPLE:MG_ROAD")
	Name     string
	Loc      LatLng
	Kind     StopKind
	// Locality or line name shown under the stop name.
	SubLabel string
}

// Route is one direction of a bus route or metro line.
type Route struct {
	ID        RouteID
	SourceID  string
	ShortName string // "500D", "Purple Line"
	Headsign  string // last stop name
	Mode      Mode
	Class     ServiceClass
	LineColor string // hex for metro; empty for bus
	Stops     []StopID
	// DistKm[i] is the cumulative distance from Stops[0] to Stops[i].
	DistKm []float64
	// Trips are explicit scheduled departures; empty when only Headways exist.
	Trips []Trip
	// Headways describe frequency-based service used when Trips is empty.
	Headways []Headway
	// StageStarts lists stop positions (>= 1) where a new BMTC fare stage
	// begins, derived from scraped fares. Empty means unknown: price by km.
	StageStarts []int
}

// StagesBetween counts fare stages traversed boarding at position from and
// alighting at to (from < to). Returns 0 when boundaries are unknown.
func (r *Route) StagesBetween(from, to int) int {
	if len(r.StageStarts) == 0 {
		return 0
	}
	n := 1
	for _, s := range r.StageStarts {
		if s > from && s <= to {
			n++
		}
	}
	return n
}

// Trip is one scheduled run along a Route. Arr[i]/Dep[i] are the arrival at
// and departure from Route.Stops[i]; both have len(Route.Stops).
type Trip struct {
	Days DayMask
	Arr  []Seconds
	Dep  []Seconds
}

// Headway is a frequency window for a route direction.
type Headway struct {
	Day   DayKind
	From  Seconds
	To    Seconds
	Every Seconds
}

// Footpath is a walking connection between two stops, used for transfers
// and metro interchanges. Stored in both directions.
type Footpath struct {
	From, To StopID
	Seconds  int32
}

// FareStageBoundary marks the first stop index of each BMTC fare stage on a
// route direction, derived from scraped fare samples.
type FareStageBoundary struct {
	RouteID RouteID
	StageNo int
	StopPos int
}

// MetroSlab is one row of the Namma Metro distance fare table.
type MetroSlab struct {
	MaxKm     float64 // upper bound inclusive; last slab uses +Inf
	FarePaise int32
}

// ClassFromRouteNumber infers the BMTC service class from the route number
// prefix, since neither the feeds nor the API carry it explicitly.
func ClassFromRouteNumber(short string) ServiceClass {
	u := strings.ToUpper(strings.TrimSpace(short))
	switch {
	case strings.HasPrefix(u, "KIA"), strings.HasPrefix(u, "VAYU"):
		return ClassVayuVajra
	case strings.HasPrefix(u, "V-"):
		return ClassVajra
	case strings.HasPrefix(u, "MF-"), strings.HasPrefix(u, "MF "):
		return ClassMetroFeeder
	}
	return ClassOrdinary
}

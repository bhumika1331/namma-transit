// Package domain holds the plain in-memory model of the transit network.
// It has no I/O; loaders (GTFS, SQLite) produce these values and the router,
// fare engine and API consume them.
package domain

import "time"

// StopID is a compact index into Network.Stops.
type StopID uint32

// RouteID is a compact index into Network.Routes. One RouteID is one
// direction of one line or bus route (GTFS "route pattern"), so UP and DOWN
// of bus 500D are two RouteIDs.
type RouteID uint32

// Minutes since local midnight (IST). Values past 1440 mean "after midnight
// of the service day", as GTFS does.
type Minutes int32

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
}

// Trip is one scheduled run along a Route. Times[i] is the departure from
// Route.Stops[i]; len(Times) == len(Route.Stops).
type Trip struct {
	Day   DayKind
	Times []Minutes
}

// Headway is a frequency window for a route direction.
type Headway struct {
	Day   DayKind
	From  Minutes
	To    Minutes
	Every Minutes
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

// Package timeprovider answers "when can I board route r at stop position p
// after time t, and when do I reach position q". The schedule-based
// implementation here uses explicit trips when a route has them and a
// headway plus speed model otherwise. A live-ETA implementation can replace
// it behind the same interface.
package timeprovider

import (
	"math"
	"sort"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// Boarding is a resolved departure the router can ride.
type Boarding struct {
	Route *domain.Route
	// TripIdx indexes Route.Trips, or -1 when synthesised from a headway.
	TripIdx int32
	// Pos is the boarding position on the route; Dep the departure there.
	Pos int32
	Dep domain.Seconds
	// Headway is non-zero when the departure is frequency-based.
	Headway domain.Seconds
	// Day the boarding was resolved for; drives the peak speed model.
	Day domain.DayKind
}

// Scheduled reports whether the boarding came from an explicit trip.
func (b Boarding) Scheduled() bool { return b.TripIdx >= 0 }

// Provider is the seam between routing and time data.
type Provider interface {
	// Board returns the earliest departure from r at position pos at or
	// after t on the given day kind.
	Board(r *domain.Route, pos int, t domain.Seconds, day domain.DayKind) (Boarding, bool)
	// Arrive returns the arrival time at position pos (> b.Pos) of the
	// boarded vehicle.
	Arrive(b Boarding, pos int) domain.Seconds
}

// Profile holds the guesstimate parameters used when no timetable exists.
type Profile struct {
	// Bus speeds in km/h and per-stop dwell.
	BusPeakKmh, BusOffPeakKmh float64
	BusDwell                  domain.Seconds
	// Metro seconds per station including dwell.
	MetroPerStation domain.Seconds
	// Peak windows on weekdays, seconds since midnight, [from, to).
	Peaks [][2]domain.Seconds
	// DefaultHeadway applies to routes with neither trips nor headways.
	DefaultHeadway domain.Seconds
	// FirstDeparture / LastDeparture bound synthesised service.
	FirstDeparture, LastDeparture domain.Seconds
}

// DefaultProfile is calibrated from Bengaluru averages: ~14 km/h bus in peak,
// ~21 km/h off-peak, 30 s dwell, metro ~1.7 min per station.
func DefaultProfile() Profile {
	return Profile{
		BusPeakKmh:      14,
		BusOffPeakKmh:   21,
		BusDwell:        30,
		MetroPerStation: 102,
		Peaks: [][2]domain.Seconds{
			{8 * 3600, 11 * 3600},
			{17 * 3600, 20 * 3600},
		},
		DefaultHeadway: 15 * 60,
		FirstDeparture: 5 * 3600,
		LastDeparture:  23 * 3600,
	}
}

// IsPeak reports whether t on the given day falls in a peak window.
func (p Profile) IsPeak(t domain.Seconds, day domain.DayKind) bool {
	if day != domain.Weekday {
		return false
	}
	tt := t % 86400
	for _, w := range p.Peaks {
		if tt >= w[0] && tt < w[1] {
			return true
		}
	}
	return false
}

// Schedule is the timetable-plus-model Provider.
type Schedule struct {
	Profile Profile
}

// NewSchedule returns a Schedule with the default profile.
func NewSchedule() *Schedule { return &Schedule{Profile: DefaultProfile()} }

// Board implements Provider.
func (s *Schedule) Board(r *domain.Route, pos int, t domain.Seconds, day domain.DayKind) (Boarding, bool) {
	if pos < 0 || pos >= len(r.Stops)-1 {
		return Boarding{}, false
	}
	if len(r.Trips) > 0 {
		return s.boardScheduled(r, pos, t, day)
	}
	return s.boardHeadway(r, pos, t, day)
}

func (s *Schedule) boardScheduled(r *domain.Route, pos int, t domain.Seconds, day domain.DayKind) (Boarding, bool) {
	// Trips are sorted by Dep[0]; assume FIFO so Dep[pos] is sorted too.
	i := sort.Search(len(r.Trips), func(i int) bool { return r.Trips[i].Dep[pos] >= t })
	for ; i < len(r.Trips); i++ {
		if r.Trips[i].Days.Has(day) {
			return Boarding{Route: r, TripIdx: int32(i), Pos: int32(pos), Dep: r.Trips[i].Dep[pos], Day: day}, true
		}
	}
	return Boarding{}, false
}

func (s *Schedule) boardHeadway(r *domain.Route, pos int, t domain.Seconds, day domain.DayKind) (Boarding, bool) {
	// Time to reach pos from the first stop shifts the service window.
	offset := s.travel(r, 0, pos, t, day)
	every, ok := s.headwayAt(r, t-offset, day)
	if !ok {
		return Boarding{}, false
	}
	// Expected wait for a random arrival is half the headway.
	dep := t + every/2
	return Boarding{Route: r, TripIdx: -1, Pos: int32(pos), Dep: dep, Headway: every, Day: day}, true
}

// headwayAt returns the service frequency at time t (departure from the
// first stop), or false when the route is not running.
func (s *Schedule) headwayAt(r *domain.Route, t domain.Seconds, day domain.DayKind) (domain.Seconds, bool) {
	if len(r.Headways) == 0 {
		if t < s.Profile.FirstDeparture || t > s.Profile.LastDeparture {
			return 0, false
		}
		return s.Profile.DefaultHeadway, true
	}
	var next domain.Seconds = math.MaxInt32
	for _, h := range r.Headways {
		if h.Day != day {
			continue
		}
		if t >= h.From && t < h.To {
			return h.Every, true
		}
		if h.From > t && h.From < next {
			next = h.From
		}
	}
	// Before the first window: wait for it to open.
	if next != math.MaxInt32 {
		for _, h := range r.Headways {
			if h.From == next && h.Day == day {
				return h.Every + (next - t), true
			}
		}
	}
	return 0, false
}

// Arrive implements Provider.
func (s *Schedule) Arrive(b Boarding, pos int) domain.Seconds {
	if b.Scheduled() {
		return b.Route.Trips[b.TripIdx].Arr[pos]
	}
	return b.Dep + s.travel(b.Route, int(b.Pos), pos, b.Dep, b.Day)
}

// travel estimates in-vehicle time between positions from the speed model.
func (s *Schedule) travel(r *domain.Route, from, to int, t domain.Seconds, day domain.DayKind) domain.Seconds {
	if to <= from {
		return 0
	}
	if r.Mode == domain.ModeMetroRail {
		return domain.Seconds(to-from) * s.Profile.MetroPerStation
	}
	km := 0.0
	if len(r.DistKm) > to {
		km = r.DistKm[to] - r.DistKm[from]
	}
	kmh := s.Profile.BusOffPeakKmh
	if s.Profile.IsPeak(t, day) {
		kmh = s.Profile.BusPeakKmh
	}
	drive := domain.Seconds(math.Round(km / kmh * 3600))
	return drive + domain.Seconds(to-from)*s.Profile.BusDwell
}

// HeadwayAround estimates the service interval of a scheduled route at
// position pos near time t, for display ("every ~N min"). Returns 0 when
// fewer than two departures exist within an hour either side.
func HeadwayAround(r *domain.Route, pos int, t domain.Seconds, day domain.DayKind) domain.Seconds {
	if len(r.Trips) == 0 {
		return 0
	}
	lo, hi := t-3600, t+3600
	var deps []domain.Seconds
	for _, tr := range r.Trips {
		if !tr.Days.Has(day) {
			continue
		}
		if d := tr.Dep[pos]; d >= lo && d <= hi {
			deps = append(deps, d)
		}
	}
	if len(deps) < 2 {
		return 0
	}
	return (deps[len(deps)-1] - deps[0]) / domain.Seconds(len(deps)-1)
}

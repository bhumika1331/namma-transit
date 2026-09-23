package timeprovider

import (
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

const (
	h  = 3600
	m  = 60
	wd = domain.Weekday
)

func scheduledRoute() *domain.Route {
	all := domain.Weekday.Bit() | domain.Saturday.Bit() | domain.Sunday.Bit()
	return &domain.Route{
		Mode:   domain.ModeBus,
		Stops:  []domain.StopID{0, 1, 2},
		DistKm: []float64{0, 2, 5},
		Trips: []domain.Trip{
			{Days: all, Arr: []domain.Seconds{8 * h, 8*h + 10*m, 8*h + 25*m}, Dep: []domain.Seconds{8 * h, 8*h + 10*m, 8*h + 25*m}},
			{Days: domain.Sunday.Bit(), Arr: []domain.Seconds{8*h + 15*m, 8*h + 25*m, 8*h + 40*m}, Dep: []domain.Seconds{8*h + 15*m, 8*h + 25*m, 8*h + 40*m}},
			{Days: all, Arr: []domain.Seconds{8*h + 30*m, 8*h + 40*m, 8*h + 55*m}, Dep: []domain.Seconds{8*h + 30*m, 8*h + 40*m, 8*h + 55*m}},
		},
	}
}

func TestBoardScheduled(t *testing.T) {
	s := NewSchedule()
	r := scheduledRoute()

	b, ok := s.Board(r, 1, 8*h+5*m, wd)
	if !ok || b.TripIdx != 0 || b.Dep != 8*h+10*m || !b.Scheduled() {
		t.Fatalf("weekday board = %+v ok=%v", b, ok)
	}
	if got := s.Arrive(b, 2); got != 8*h+25*m {
		t.Fatalf("arrive = %d", got)
	}
	// Just missed trip 0 at pos 1; Sunday-only trip must be skipped on a weekday.
	b, ok = s.Board(r, 1, 8*h+11*m, wd)
	if !ok || b.TripIdx != 2 {
		t.Fatalf("skip sunday trip: %+v", b)
	}
	b, ok = s.Board(r, 1, 8*h+11*m, domain.Sunday)
	if !ok || b.TripIdx != 1 {
		t.Fatalf("sunday board: %+v", b)
	}
	if _, ok := s.Board(r, 1, 9*h, wd); ok {
		t.Fatal("no trips after last departure")
	}
	if _, ok := s.Board(r, 2, 8*h, wd); ok {
		t.Fatal("cannot board at the last stop")
	}
}

func TestBoardHeadwayAndSpeedModel(t *testing.T) {
	s := NewSchedule()
	r := &domain.Route{
		Mode:   domain.ModeBus,
		Stops:  []domain.StopID{0, 1, 2},
		DistKm: []float64{0, 3.5, 7},
		Headways: []domain.Headway{
			{Day: wd, From: 6 * h, To: 22 * h, Every: 10 * m},
		},
	}
	// Peak (09:00): 7 km at 14 km/h = 30 min + 2 dwells = 31 min; wait = 5 min.
	b, ok := s.Board(r, 0, 9*h, wd)
	if !ok || b.Scheduled() || b.Headway != 10*m || b.Dep != 9*h+5*m {
		t.Fatalf("peak board = %+v ok=%v", b, ok)
	}
	if got := s.Arrive(b, 2) - b.Dep; got != 31*m {
		t.Fatalf("peak travel = %d s, want 31 min", got)
	}
	// Off-peak (13:00): 7 km at 21 km/h = 20 min + 1 min dwell.
	b, _ = s.Board(r, 0, 13*h, wd)
	if got := s.Arrive(b, 2) - b.Dep; got != 21*m {
		t.Fatalf("off-peak travel = %d s, want 21 min", got)
	}
	// Outside the window: not running.
	if _, ok := s.Board(r, 0, 23*h, wd); ok {
		t.Fatal("should not run at 23:00")
	}
	// Before the window opens at 06:00: wait until it does.
	b, ok = s.Board(r, 0, 5*h+50*m, wd)
	if !ok || b.Dep < 6*h {
		t.Fatalf("pre-window board = %+v ok=%v", b, ok)
	}
}

func TestMetroSynthesised(t *testing.T) {
	s := NewSchedule()
	r := &domain.Route{Mode: domain.ModeMetroRail, Stops: make([]domain.StopID, 10)}
	b, ok := s.Board(r, 2, 10*h, wd)
	if !ok || b.Headway != s.Profile.DefaultHeadway {
		t.Fatalf("metro default headway board = %+v ok=%v", b, ok)
	}
	// 7 stations at 102 s each.
	if got := s.Arrive(b, 9) - b.Dep; got != 7*102 {
		t.Fatalf("metro travel = %d", got)
	}
}

func TestHeadwayAround(t *testing.T) {
	r := scheduledRoute()
	// Weekday departures at pos 0 within an hour of 08:15: 08:00 and 08:30.
	if got := HeadwayAround(r, 0, 8*h+15*m, wd); got != 30*m {
		t.Fatalf("headway = %d, want 30 min", got)
	}
	if got := HeadwayAround(r, 0, 8*h+15*m, domain.Sunday); got != 15*m {
		t.Fatalf("sunday headway = %d, want 15 min", got)
	}
	if got := HeadwayAround(r, 0, 20*h, wd); got != 0 {
		t.Fatalf("no service headway = %d", got)
	}
}

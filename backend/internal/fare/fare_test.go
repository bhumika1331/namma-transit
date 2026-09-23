package fare

import (
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

func engine(t *testing.T) *Engine {
	tb, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Tables: tb}
}

func TestOrdinaryChartBoundaries(t *testing.T) {
	e := engine(t)
	cases := []struct {
		km   float64
		want int32
	}{
		{0.5, 600}, {2, 600}, {2.01, 1200}, {4, 1200}, {5.9, 1800}, {6.5, 2300}, {10, 2300},
		{12, 2400}, {14.1, 2800}, {20, 2800}, {25, 3000}, {40, 3000}, {45, 3200}, {80, 3200},
	}
	for _, c := range cases {
		if got := e.Tables.Ordinary.ByKm(c.km); got != c.want {
			t.Errorf("ordinary %.2f km = %d, want %d", c.km, got, c.want)
		}
	}
	if got := e.Tables.Ordinary.ByStages(3); got != 1800 {
		t.Errorf("3 stages = %d", got)
	}
	if got := e.Tables.Ordinary.ByStages(0); got != 600 {
		t.Errorf("0 stages should charge minimum, got %d", got)
	}
}

func TestVajraAndMetroCharts(t *testing.T) {
	e := engine(t)
	if got := e.Tables.Vajra.ByKm(9); got != 3000 {
		t.Errorf("vajra 9 km = %d", got)
	}
	if got := e.Tables.Vajra.ByKm(60); got != 6500 {
		t.Errorf("vajra 60 km = %d", got)
	}
	metro := []struct {
		km   float64
		want int32
	}{{1, 1000}, {3.9, 2000}, {8, 4000}, {9, 5000}, {12, 6000}, {19, 7000}, {24, 8000}, {30, 9000}, {45, 9000}}
	for _, c := range metro {
		if got := e.Tables.Metro.ByKm(c.km); got != c.want {
			t.Errorf("metro %.1f km = %d, want %d", c.km, got, c.want)
		}
	}
}

func TestShaktiSplit(t *testing.T) {
	e := engine(t)
	p := Party{Total: 5, Women: 3}
	lp := e.PriceLeg(Leg{Class: domain.ClassOrdinary, Km: 7}, p)
	if !lp.WomenFree || lp.PayingRiders != 2 || lp.PerPerson != 2300 || lp.Group != 4600 || lp.Stages != 4 {
		t.Fatalf("ordinary 5/3 = %+v", lp)
	}
	lp = e.PriceLeg(Leg{Class: domain.ClassVajra, Km: 7}, p)
	if lp.WomenFree || lp.PayingRiders != 5 || lp.Group != 5*3000 {
		t.Fatalf("vajra 5/3 = %+v", lp)
	}
	lp = e.PriceLeg(Leg{Class: domain.ClassMetroFeeder, Km: 3}, Party{Total: 2, Women: 2})
	if lp.Group != 0 || lp.PayingRiders != 0 {
		t.Fatalf("all-women feeder = %+v", lp)
	}
	// Women count larger than total is clamped.
	lp = e.PriceLeg(Leg{Class: domain.ClassOrdinary, Km: 3}, Party{Total: 1, Women: 4})
	if lp.PayingRiders != 0 || lp.Group != 0 {
		t.Fatalf("clamp = %+v", lp)
	}
}

func TestMetroSmartCard(t *testing.T) {
	e := engine(t)
	// 9 km -> ₹50. Off-peak 10% -> ₹45; peak 5% -> ₹47.50 floored to ₹47.
	lp := e.PriceLeg(Leg{Class: domain.ClassMetro, Km: 9}, Party{Total: 3, Women: 1, SmartCards: 2})
	if lp.WomenFree || lp.PayingRiders != 3 || lp.SmartCardSaving != 1000 || lp.Group != 3*5000-1000 {
		t.Fatalf("off-peak = %+v", lp)
	}
	lp = e.PriceLeg(Leg{Class: domain.ClassMetro, Km: 9, Peak: true}, Party{Total: 3, SmartCards: 5})
	if lp.SmartCardSaving != 3*300 || lp.Group != 15000-900 {
		t.Fatalf("peak, more cards than riders = %+v", lp)
	}
}

func TestPriceTotalsAndPassHint(t *testing.T) {
	e := engine(t)
	p := Party{Total: 4, Women: 2}
	legs := []Leg{
		{Class: domain.ClassOrdinary, Km: 3},   // ₹12, 2 pay
		{Class: domain.ClassMetro, Km: 12},     // ₹60, 4 pay
		{Class: domain.ClassOrdinary, Km: 1.5}, // ₹6, 2 pay
	}
	tot := e.Price(legs, p)
	if tot.Group != 2*1200+4*6000+2*600 {
		t.Fatalf("group = %d", tot.Group)
	}
	if tot.PerWoman != 6000 || tot.PerOther != 1200+6000+600 {
		t.Fatalf("per woman %d per other %d", tot.PerWoman, tot.PerOther)
	}
	if tot.DailyPassHint {
		t.Fatalf("two cheap bus legs should not suggest a pass: %+v", tot)
	}
	// Three ordinary legs -> hint with pass for the 2 paying riders.
	tot = e.Price([]Leg{{Class: domain.ClassOrdinary, Km: 3}, {Class: domain.ClassOrdinary, Km: 3}, {Class: domain.ClassOrdinary, Km: 3}}, p)
	if !tot.DailyPassHint || tot.DailyPassGroup != 2*8000 {
		t.Fatalf("pass hint = %+v", tot)
	}
	// Two Vajra legs at ₹65 each beat a ₹140 pass -> no hint; three -> hint for all 4.
	tot = e.Price([]Leg{{Class: domain.ClassVajra, Km: 48}, {Class: domain.ClassVajra, Km: 48}}, p)
	if tot.DailyPassHint {
		t.Fatalf("2x65 < 140, no hint: %+v", tot)
	}
	tot = e.Price([]Leg{{Class: domain.ClassVajra, Km: 48}, {Class: domain.ClassVajra, Km: 48}, {Class: domain.ClassVajra, Km: 1}}, p)
	if !tot.DailyPassHint || tot.DailyPassGroup != 4*14000 {
		t.Fatalf("vajra pass hint = %+v", tot)
	}
}

func TestAutoBaseline(t *testing.T) {
	e := engine(t)
	// 5 km straight -> 6.5 km road -> 36 + 5*18 = ₹126 per auto; 4 people = 2 autos.
	ab := e.Auto(5, 4, false, false)
	if ab.Autos != 2 || ab.Group != 2*12600 || ab.RoadKm != 6.5 {
		t.Fatalf("auto = %+v", ab)
	}
	if ab.EstMinutes != 16 {
		t.Fatalf("off-peak minutes = %d", ab.EstMinutes)
	}
	if got := e.Auto(5, 3, true, true); got.Autos != 1 || got.Group != 18900 || got.EstMinutes != 22 {
		t.Fatalf("night peak = %+v", got)
	}
	if got := e.Auto(1, 1, false, false); got.Group != 3600 {
		t.Fatalf("within base km = %+v", got)
	}
}

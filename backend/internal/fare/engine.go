package fare

import (
	"math"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// Party mirrors the request: how many ride, how many are women, how many
// hold a metro smart card.
type Party struct {
	Total, Women, SmartCards int
}

// Leg is the pricing view of one vehicle leg.
type Leg struct {
	Class domain.ServiceClass
	Km    float64
	// Stages is the BMTC fare-stage count when known from scraped boundaries;
	// 0 means "derive from Km".
	Stages int
	// Peak affects only the metro smart-card discount.
	Peak bool
}

// LegPrice is the per-person and group outcome for one leg.
type LegPrice struct {
	Class        domain.ServiceClass
	PerPerson    int32 // full adult fare, paise
	PayingRiders int
	Group        int32
	// SmartCardSaving is the total discount applied inside Group.
	SmartCardSaving int32
	WomenFree       bool
	Stages          int
}

// Total is the priced itinerary.
type Total struct {
	Legs            []LegPrice
	Group           int32
	PerWoman        int32
	PerOther        int32
	SmartCardSaving int32
	DailyPassHint   bool
	DailyPassGroup  int32
}

// Engine prices legs with a set of tables.
type Engine struct {
	Tables *Tables
}

// PriceLeg computes one leg's cost for the party.
func (e *Engine) PriceLeg(l Leg, p Party) LegPrice {
	chart := e.Tables.ChartFor(l.Class)
	var per int32
	stages := l.Stages
	if l.Class == domain.ClassMetro {
		per = chart.ByKm(l.Km)
	} else if stages > 0 {
		per = chart.ByStages(stages)
	} else {
		per = chart.ByKm(l.Km)
		if chart.StageKm > 0 {
			stages = int(math.Max(1, math.Ceil(l.Km/chart.StageKm-1e-9)))
		}
	}
	out := LegPrice{Class: l.Class, PerPerson: per, Stages: stages}
	paying := p.Total
	if l.Class.WomenFree() {
		out.WomenFree = true
		paying = max(0, p.Total-p.Women)
	}
	out.PayingRiders = paying
	out.Group = per * int32(paying)
	if l.Class == domain.ClassMetro && p.SmartCards > 0 {
		pct := chart.SmartCardOffPeakPct
		if l.Peak {
			pct = chart.SmartCardPeakPct
		}
		holders := min(p.SmartCards, paying)
		// BMRCL rounds the discounted fare down to the rupee.
		disc := per - int32(math.Floor(float64(per)*float64(100-pct)/100/100)*100)
		out.SmartCardSaving = disc * int32(holders)
		out.Group -= out.SmartCardSaving
	}
	return out
}

// Price prices a whole itinerary.
func (e *Engine) Price(legs []Leg, p Party) Total {
	var t Total
	var busOrdinaryPer, busVajraPer int32
	busLegs := 0
	for _, l := range legs {
		lp := e.PriceLeg(l, p)
		t.Legs = append(t.Legs, lp)
		t.Group += lp.Group
		t.SmartCardSaving += lp.SmartCardSaving
		if !lp.WomenFree {
			t.PerWoman += lp.PerPerson
		}
		t.PerOther += lp.PerPerson
		switch l.Class {
		case domain.ClassOrdinary, domain.ClassMetroFeeder:
			busOrdinaryPer += lp.PerPerson
			busLegs++
		case domain.ClassVajra, domain.ClassVayuVajra:
			busVajraPer += lp.PerPerson
			busLegs++
		}
	}
	// Daily pass hint: when the summed bus fares beat the pass, or the trip
	// already has 3+ bus legs (a day with a return trip surely will).
	ord, vaj := e.Tables.Ordinary, e.Tables.Vajra
	if busLegs > 0 {
		payingOrd := max(0, p.Total-p.Women)
		var passGroup, busGroup int32
		if busVajraPer > 0 {
			passGroup = vaj.DailyPassPaise * int32(p.Total)
			busGroup = busVajraPer*int32(p.Total) + busOrdinaryPer*int32(payingOrd)
		} else {
			passGroup = ord.DailyPassPaise * int32(payingOrd)
			busGroup = busOrdinaryPer * int32(payingOrd)
		}
		if passGroup > 0 && (busLegs >= 3 || busGroup > passGroup) {
			t.DailyPassHint = true
			t.DailyPassGroup = passGroup
		}
	}
	return t
}

// AutoBaseline is the comparison line for taking metered autos.
type AutoBaseline struct {
	Autos      int
	RoadKm     float64
	Group      int32
	EstMinutes int
}

// Auto estimates the auto cost for the party over a straight-line distance.
func (e *Engine) Auto(straightKm float64, total int, night, peak bool) AutoBaseline {
	a := e.Tables.Auto
	autos := int(math.Ceil(float64(total) / float64(a.Seats)))
	if autos < 1 {
		autos = 1
	}
	road := straightKm * a.RoadFactor
	kmh := a.OffpeakKmh
	if peak {
		kmh = a.PeakKmh
	}
	return AutoBaseline{
		Autos:      autos,
		RoadKm:     road,
		Group:      a.Fare(road, night) * int32(autos),
		EstMinutes: int(math.Ceil(road / kmh * 60)),
	}
}

// Package fare prices itineraries: per-leg fares by service class, the Shakti
// rule for women on non-AC buses, metro smart-card discounts, group totals,
// daily-pass hints and the metered-auto baseline.
package fare

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// Slab is one row of a distance fare table.
type Slab struct {
	MaxKm      float64 `json:"max_km"`
	AdultPaise int32   `json:"adult_paise"`
}

// Chart is a versioned distance-based fare table.
type Chart struct {
	Class               string  `json:"class"`
	EffectiveFrom       string  `json:"effective_from"`
	Source              string  `json:"source"`
	StageKm             float64 `json:"stage_km"`
	Slabs               []Slab  `json:"slabs"`
	DailyPassPaise      int32   `json:"daily_pass_paise"`
	SmartCardPeakPct    int32   `json:"smart_card_peak_discount_pct"`
	SmartCardOffPeakPct int32   `json:"smart_card_offpeak_discount_pct"`
}

// ByKm returns the adult fare for a distance; distances beyond the last slab
// pay the last slab.
func (c *Chart) ByKm(km float64) int32 {
	if len(c.Slabs) == 0 {
		return 0
	}
	for _, s := range c.Slabs {
		if km <= s.MaxKm+1e-9 {
			return s.AdultPaise
		}
	}
	return c.Slabs[len(c.Slabs)-1].AdultPaise
}

// ByStages prices a BMTC leg by the number of fare stages traversed.
func (c *Chart) ByStages(stages int) int32 {
	if stages < 1 {
		stages = 1
	}
	return c.ByKm(float64(stages) * c.StageKm)
}

// AutoTariff is the metered auto-rickshaw tariff.
type AutoTariff struct {
	EffectiveFrom   string  `json:"effective_from"`
	Source          string  `json:"source"`
	BasePaise       int32   `json:"base_paise"`
	BaseKm          float64 `json:"base_km"`
	PerKmPaise      int32   `json:"per_km_paise"`
	NightMultiplier float64 `json:"night_multiplier"`
	NightFrom       string  `json:"night_from"`
	NightTo         string  `json:"night_to"`
	Seats           int     `json:"seats"`
	RoadFactor      float64 `json:"road_factor"`
	PeakKmh         float64 `json:"peak_kmh"`
	OffpeakKmh      float64 `json:"offpeak_kmh"`
}

// Fare for one auto over a road distance.
func (a AutoTariff) Fare(roadKm float64, night bool) int32 {
	extra := math.Max(0, roadKm-a.BaseKm)
	f := float64(a.BasePaise) + math.Ceil(extra)*float64(a.PerKmPaise)
	if night {
		f *= a.NightMultiplier
	}
	return int32(math.Round(f/100) * 100) // autos round to the rupee
}

// Tables bundles every chart the engine needs.
type Tables struct {
	Ordinary *Chart
	Vajra    *Chart
	Metro    *Chart
	Auto     AutoTariff
}

//go:embed all:charts
var embedded embed.FS

// LoadEmbedded reads the charts compiled into the binary from
// backend/internal/fare/charts (a copy of backend/data/fares).
func LoadEmbedded() (*Tables, error) {
	t := &Tables{}
	load := func(name string, v any) error {
		b, err := embedded.ReadFile("charts/" + name)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	t.Ordinary, t.Vajra, t.Metro = &Chart{}, &Chart{}, &Chart{}
	if err := load("bmtc_ordinary.json", t.Ordinary); err != nil {
		return nil, err
	}
	if err := load("bmtc_vajra.json", t.Vajra); err != nil {
		return nil, err
	}
	if err := load("metro.json", t.Metro); err != nil {
		return nil, err
	}
	if err := load("auto.json", &t.Auto); err != nil {
		return nil, err
	}
	return t, nil
}

// ChartFor picks the chart for a service class. Vayu Vajra falls back to the
// Vajra chart until per-route fares are scraped.
func (t *Tables) ChartFor(c domain.ServiceClass) *Chart {
	switch c {
	case domain.ClassVajra, domain.ClassVayuVajra:
		return t.Vajra
	case domain.ClassMetro:
		return t.Metro
	default:
		return t.Ordinary
	}
}

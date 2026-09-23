// Package geo has the small amount of geometry the planner needs: great-circle
// distance, a grid index for nearby-stop lookup, and footpath generation.
package geo

import (
	"math"
	"sort"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

const earthRadiusM = 6371000.0

// DistanceM returns the great-circle distance between two points in metres.
func DistanceM(a, b domain.LatLng) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := lat2 - lat1
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// WalkSpeedMPS is the walking speed used for footpaths and access legs.
const WalkSpeedMPS = 4.5 * 1000 / 3600

// WalkSeconds converts a distance to walking time.
func WalkSeconds(distM float64) int32 {
	return int32(math.Ceil(distM / WalkSpeedMPS))
}

// cellDeg is the grid cell size in degrees; ~550 m of latitude in Bengaluru.
const cellDeg = 0.005

type cellKey struct{ x, y int32 }

// GridIndex buckets stops into fixed-size lat/lng cells for cheap radius
// queries. Build once; it is read-only afterwards and safe for concurrent use.
type GridIndex struct {
	stops []domain.Stop
	cells map[cellKey][]domain.StopID
}

func cellOf(p domain.LatLng) cellKey {
	return cellKey{
		x: int32(math.Floor(p.Lng / cellDeg)),
		y: int32(math.Floor(p.Lat / cellDeg)),
	}
}

// NewGridIndex indexes stops by position. Stop IDs must equal their index.
func NewGridIndex(stops []domain.Stop) *GridIndex {
	g := &GridIndex{stops: stops, cells: make(map[cellKey][]domain.StopID, len(stops)/4+1)}
	for _, s := range stops {
		k := cellOf(s.Loc)
		g.cells[k] = append(g.cells[k], s.ID)
	}
	return g
}

// Near is a stop within a radius query, with its distance from the query point.
type Near struct {
	Stop  domain.StopID
	DistM float64
}

// Within returns all stops within radiusM of p, sorted by distance.
func (g *GridIndex) Within(p domain.LatLng, radiusM float64) []Near {
	// Number of cells to scan on each side; latitude degree ~111 km.
	span := int32(math.Ceil(radiusM/(cellDeg*111000))) + 1
	c := cellOf(p)
	var out []Near
	for dx := -span; dx <= span; dx++ {
		for dy := -span; dy <= span; dy++ {
			for _, id := range g.cells[cellKey{c.x + dx, c.y + dy}] {
				d := DistanceM(p, g.stops[id].Loc)
				if d <= radiusM {
					out = append(out, Near{Stop: id, DistM: d})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistM < out[j].DistM })
	return out
}

// Footpaths generates walking transfers between every pair of distinct stops
// within maxM of each other, in both directions. Stops within the same
// metro station group are handled separately by the loader; this is the
// generic geometric pass.
func Footpaths(g *GridIndex, maxM float64) []domain.Footpath {
	var out []domain.Footpath
	for _, s := range g.stops {
		for _, n := range g.Within(s.Loc, maxM) {
			if n.Stop == s.ID {
				continue
			}
			out = append(out, domain.Footpath{From: s.ID, To: n.Stop, Seconds: WalkSeconds(n.DistM)})
		}
	}
	return out
}

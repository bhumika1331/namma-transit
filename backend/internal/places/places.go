// Package places resolves free text to stops and stations. This first
// version matches stop names; a later task adds fuzzy matching and an
// external geocoder fallback behind the Geocoder interface.
package places

import (
	"context"
	"sort"
	"strings"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
)

// Geocoder is the external fallback for names that are not stops.
type Geocoder interface {
	Suggest(ctx context.Context, query string, bias *domain.LatLng, limit int) ([]*transitv1.Place, error)
}

type entry struct {
	lower string
	tri   map[string]struct{}
	place *transitv1.Place
}

// trigrams of a lowercased, padded string; used for typo-tolerant matching.
func trigrams(s string) map[string]struct{} {
	s = "  " + s + " "
	out := make(map[string]struct{}, len(s))
	for i := 0; i+3 <= len(s); i++ {
		out[s[i:i+3]] = struct{}{}
	}
	return out
}

func similarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := 0
	for t := range a {
		if _, ok := b[t]; ok {
			n++
		}
	}
	return float64(n) / float64(len(a)+len(b)-n)
}

// Index is a read-only name index over the network's stops.
type Index struct {
	entries []entry
	byID    map[string]domain.StopID
	geo     Geocoder // may be nil
}

// New builds the index. Stops sharing a name and kind collapse into one
// suggestion (a metro station on two lines, a bus stop with two platforms).
func New(n *network.Network, geo Geocoder) *Index {
	idx := &Index{byID: make(map[string]domain.StopID, len(n.Stops)), geo: geo}
	seen := map[string]bool{}
	for _, s := range n.Stops {
		idx.byID[s.SourceID] = s.ID
		key := strings.ToLower(s.Name) + "|" + kindOf(s).String()
		if seen[key] {
			continue
		}
		seen[key] = true
		lower := strings.ToLower(s.Name)
		idx.entries = append(idx.entries, entry{lower: lower, tri: trigrams(lower), place: ToPlace(s)})
	}
	sort.Slice(idx.entries, func(i, j int) bool { return idx.entries[i].lower < idx.entries[j].lower })
	return idx
}

func kindOf(s domain.Stop) transitv1.PlaceKind {
	if s.Kind == domain.StopMetro {
		return transitv1.PlaceKind_PLACE_KIND_METRO_STATION
	}
	return transitv1.PlaceKind_PLACE_KIND_BUS_STOP
}

// ToPlace converts a stop to its API representation.
func ToPlace(s domain.Stop) *transitv1.Place {
	return &transitv1.Place{
		Id:       s.SourceID,
		Name:     s.Name,
		Kind:     kindOf(s),
		Loc:      &transitv1.LatLng{Lat: s.Loc.Lat, Lng: s.Loc.Lng},
		SubLabel: s.SubLabel,
	}
}

// Lookup returns the stop for a place id.
func (idx *Index) Lookup(id string) (domain.StopID, bool) {
	s, ok := idx.byID[id]
	return s, ok
}

// minFuzzy is the trigram similarity below which a stop is not offered.
const minFuzzy = 0.34

// Suggest ranks stops whose name starts with, then contains, then merely
// resembles the query (trigram similarity, for "koramangla"). Metro
// stations rank above bus stops on ties; shorter names first. When nothing
// matches well and a geocoder is configured, its results are returned with
// fromGeocoder=true.
func (idx *Index) Suggest(ctx context.Context, query string, bias *domain.LatLng, limit int) ([]*transitv1.Place, bool, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 || limit > 10 {
		limit = 6
	}
	if len(q) < 2 {
		return nil, false, nil
	}
	type scored struct {
		score float64
		p     *transitv1.Place
	}
	qt := trigrams(q)
	var hits []scored
	for _, e := range idx.entries {
		var score float64
		switch {
		case strings.HasPrefix(e.lower, q):
			score = 3
		case strings.Contains(e.lower, " "+q):
			score = 2
		case strings.Contains(e.lower, q):
			score = 1
		default:
			if sim := similarity(qt, e.tri); sim >= minFuzzy {
				score = sim // (0.34, 1)
			} else {
				continue
			}
		}
		if e.place.Kind == transitv1.PlaceKind_PLACE_KIND_METRO_STATION {
			score += 10
		}
		hits = append(hits, scored{score, e.place})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return len(hits[i].p.Name) < len(hits[j].p.Name)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]*transitv1.Place, len(hits))
	for i, h := range hits {
		out[i] = h.p
	}
	// Strong stop matches win; otherwise ask the geocoder for landmarks and
	// localities ("Phoenix Mall", "Koramangala 5th Block").
	strong := len(hits) > 0 && hits[0].score >= 1
	if !strong && idx.geo != nil {
		places, err := idx.geo.Suggest(ctx, query, bias, limit)
		if err == nil && len(places) > 0 {
			return places, true, nil
		}
		if len(out) == 0 {
			return nil, true, err
		}
	}
	return out, false, nil
}

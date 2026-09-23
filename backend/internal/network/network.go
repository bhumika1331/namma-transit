// Package network assembles loaded datasets into the frozen in-memory graph
// the router and planner query.
package network

import (
	"errors"
	"sort"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/geo"
)

// Source is anything that can supply a dataset: a GTFS feed or the SQLite
// store filled by the scraper. IDs inside a Source are local to it and are
// re-based during Build.
type Source interface {
	Name() string    // "bmtc", "bmrcl"
	Source() string  // "gtfs", "scrape"
	Version() string // feed version or scrape timestamp
	Stops() []domain.Stop
	Routes() []domain.Route
	Footpaths() []domain.Footpath
}

// DatasetInfo is reported by Health.
type DatasetInfo struct {
	Name, Source, Version string
	Stops, Routes, Trips  int
}

// RouteStop says that route Route visits the stop at position Pos.
type RouteStop struct {
	Route domain.RouteID
	Pos   int32
}

// Network is read-only after Build and safe for concurrent use.
type Network struct {
	Stops      []domain.Stop
	Routes     []domain.Route
	StopRoutes [][]RouteStop // per stop
	// Footpaths in CSR form: outgoing footpaths of stop s are
	// Foot[FootIdx[s]:FootIdx[s+1]].
	Foot    []domain.Footpath
	FootIdx []int32
	Grid    *geo.GridIndex

	Datasets []DatasetInfo
	LoadedAt time.Time
	// Version summarises all dataset versions for PlanTripResponse.data_version.
	Version string
}

// BuildOptions tunes graph construction.
type BuildOptions struct {
	// FootpathMaxM is the maximum walking distance between two stops that
	// yields a transfer footpath. Default 300.
	FootpathMaxM float64
}

// Build merges sources, re-bases their IDs and derives footpaths.
func Build(sources []Source, opts BuildOptions) (*Network, error) {
	if len(sources) == 0 {
		return nil, errors.New("no sources")
	}
	if opts.FootpathMaxM == 0 {
		opts.FootpathMaxM = 300
	}
	n := &Network{LoadedAt: time.Now()}
	var explicit []domain.Footpath
	for _, src := range sources {
		stopBase := domain.StopID(len(n.Stops))
		routeBase := domain.RouteID(len(n.Routes))
		trips := 0
		for _, s := range src.Stops() {
			s.ID += stopBase
			n.Stops = append(n.Stops, s)
		}
		for _, r := range src.Routes() {
			r.ID += routeBase
			stops := make([]domain.StopID, len(r.Stops))
			for i, id := range r.Stops {
				stops[i] = id + stopBase
			}
			r.Stops = stops
			trips += len(r.Trips)
			n.Routes = append(n.Routes, r)
		}
		for _, f := range src.Footpaths() {
			f.From += stopBase
			f.To += stopBase
			explicit = append(explicit, f)
		}
		n.Datasets = append(n.Datasets, DatasetInfo{
			Name: src.Name(), Source: src.Source(), Version: src.Version(),
			Stops: len(src.Stops()), Routes: len(src.Routes()), Trips: trips,
		})
		if n.Version != "" {
			n.Version += "+"
		}
		n.Version += src.Name() + "@" + src.Version()
	}

	n.StopRoutes = make([][]RouteStop, len(n.Stops))
	for _, r := range n.Routes {
		for pos, s := range r.Stops {
			n.StopRoutes[s] = append(n.StopRoutes[s], RouteStop{Route: r.ID, Pos: int32(pos)})
		}
	}

	n.Grid = geo.NewGridIndex(n.Stops)
	n.buildFootpaths(explicit, geo.Footpaths(n.Grid, opts.FootpathMaxM))
	return n, nil
}

// buildFootpaths merges explicit and geometric footpaths (keeping the
// shorter of duplicates) into CSR arrays.
func (n *Network) buildFootpaths(lists ...[]domain.Footpath) {
	type key struct{ from, to domain.StopID }
	best := map[key]int32{}
	for _, list := range lists {
		for _, f := range list {
			if f.From == f.To {
				continue
			}
			k := key{f.From, f.To}
			if cur, ok := best[k]; !ok || f.Seconds < cur {
				best[k] = f.Seconds
			}
		}
	}
	n.Foot = make([]domain.Footpath, 0, len(best))
	for k, s := range best {
		n.Foot = append(n.Foot, domain.Footpath{From: k.from, To: k.to, Seconds: s})
	}
	sort.Slice(n.Foot, func(i, j int) bool {
		if n.Foot[i].From != n.Foot[j].From {
			return n.Foot[i].From < n.Foot[j].From
		}
		return n.Foot[i].To < n.Foot[j].To
	})
	n.FootIdx = make([]int32, len(n.Stops)+1)
	for _, f := range n.Foot {
		n.FootIdx[f.From+1]++
	}
	for i := 1; i < len(n.FootIdx); i++ {
		n.FootIdx[i] += n.FootIdx[i-1]
	}
}

// FootpathsFrom returns the outgoing footpaths of a stop.
func (n *Network) FootpathsFrom(s domain.StopID) []domain.Footpath {
	return n.Foot[n.FootIdx[s]:n.FootIdx[s+1]]
}

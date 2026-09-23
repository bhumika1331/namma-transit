// Package raptor implements the RAPTOR public-transit routing algorithm
// (Delling, Pajor, Werneck 2012) over the in-memory Network. It returns the
// Pareto set of journeys on (arrival time, number of rides) for a departure
// time; the planner prices and re-ranks them.
package raptor

import (
	"math"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

const inf = domain.Seconds(math.MaxInt32)

// Access is a stop reachable on foot from the origin (or reaching the
// destination) with the walking time.
type Access struct {
	Stop    domain.StopID
	Seconds domain.Seconds
}

// Query is one earliest-arrival request.
type Query struct {
	Sources []Access
	Targets []Access
	Depart  domain.Seconds
	Day     domain.DayKind
	// MaxRides bounds the number of vehicle legs (transfers + 1). Default 2.
	MaxRides int
	// Modes restricts which route modes may be ridden; nil allows all. The
	// planner uses this to surface a bus-only alternative that the metro
	// would otherwise dominate.
	Modes []domain.Mode
	// ExcludeClasses drops routes of these service classes entirely.
	ExcludeClasses []domain.ServiceClass
}

func (q Query) allows(r *domain.Route) bool {
	for _, c := range q.ExcludeClasses {
		if r.Class == c {
			return false
		}
	}
	if len(q.Modes) == 0 {
		return true
	}
	for _, x := range q.Modes {
		if x == r.Mode {
			return true
		}
	}
	return false
}

// LegKind distinguishes walking from riding.
type LegKind uint8

const (
	LegWalk LegKind = iota + 1
	LegRide
)

// Leg is one segment of a journey. Walking legs to/from the origin and
// destination have From/To set to the stop and the other end implied.
type Leg struct {
	Kind LegKind
	// Ride fields.
	Route    domain.RouteID
	Boarding timeprovider.Boarding
	FromPos  int
	ToPos    int
	// Shared.
	From, To domain.StopID // for access/egress walks From==To==the stop
	Dep, Arr domain.Seconds
}

// Journey is a full origin-to-destination result.
type Journey struct {
	Legs   []Leg
	Depart domain.Seconds // leaving the origin
	Arrive domain.Seconds // reaching the destination
	Rides  int
}

// Router runs queries against one network. It is stateless between queries.
type Router struct {
	Net  *network.Network
	Time timeprovider.Provider
}

type parentKind uint8

const (
	parentNone parentKind = iota
	parentAccess
	parentRide
	parentWalk
)

type parent struct {
	kind     parentKind
	from     domain.StopID
	boarding timeprovider.Boarding
	fromPos  int
	toPos    int
	walk     domain.Seconds
}

// Plan returns Pareto-optimal journeys, fewest rides first.
func (r *Router) Plan(q Query) []Journey {
	if q.MaxRides <= 0 {
		q.MaxRides = 2
	}
	n := r.Net
	nStops := len(n.Stops)
	rounds := q.MaxRides + 1

	tau := make([][]domain.Seconds, rounds)
	par := make([][]parent, rounds)
	for k := range tau {
		tau[k] = make([]domain.Seconds, nStops)
		for i := range tau[k] {
			tau[k][i] = inf
		}
		par[k] = make([]parent, nStops)
	}
	best := make([]domain.Seconds, nStops)
	for i := range best {
		best[i] = inf
	}
	marked := make([]bool, nStops)
	var markedList []domain.StopID

	egress := make(map[domain.StopID]domain.Seconds, len(q.Targets))
	for _, t := range q.Targets {
		if cur, ok := egress[t.Stop]; !ok || t.Seconds < cur {
			egress[t.Stop] = t.Seconds
		}
	}
	bestTarget := inf

	mark := func(s domain.StopID) {
		if !marked[s] {
			marked[s] = true
			markedList = append(markedList, s)
		}
	}

	// Round 0: walk from origin.
	for _, a := range q.Sources {
		t := q.Depart + a.Seconds
		if t < tau[0][a.Stop] {
			tau[0][a.Stop] = t
			best[a.Stop] = t
			par[0][a.Stop] = parent{kind: parentAccess, walk: a.Seconds}
			mark(a.Stop)
			if e, ok := egress[a.Stop]; ok && t+e < bestTarget {
				bestTarget = t + e
			}
		}
	}
	r.relaxFootpaths(0, tau, par, best, marked, &markedList, egress, &bestTarget)

	// Per-route earliest marked position, reused each round.
	routePos := make([]int32, len(n.Routes))

	for k := 1; k < rounds; k++ {
		// Collect routes touching marked stops.
		for i := range routePos {
			routePos[i] = -1
		}
		var routes []domain.RouteID
		for _, s := range markedList {
			for _, rs := range n.StopRoutes[s] {
				if routePos[rs.Route] < 0 {
					routes = append(routes, rs.Route)
					routePos[rs.Route] = rs.Pos
				} else if rs.Pos < routePos[rs.Route] {
					routePos[rs.Route] = rs.Pos
				}
			}
			marked[s] = false
		}
		markedList = markedList[:0]

		for _, rid := range routes {
			route := &n.Routes[rid]
			if !q.allows(route) {
				continue
			}
			var b timeprovider.Boarding
			hasB := false
			var bStop domain.StopID
			var bPos int
			for p := int(routePos[rid]); p < len(route.Stops); p++ {
				s := route.Stops[p]
				if hasB && p > bPos {
					arr := r.Time.Arrive(b, p)
					if arr < best[s] && arr < bestTarget {
						tau[k][s] = arr
						best[s] = arr
						par[k][s] = parent{kind: parentRide, from: bStop, boarding: b, fromPos: bPos, toPos: p}
						mark(s)
						if e, ok := egress[s]; ok && arr+e < bestTarget {
							bestTarget = arr + e
						}
					}
				}
				// Can we board (an earlier vehicle) here?
				prev := tau[k-1][s]
				if prev == inf || p == len(route.Stops)-1 {
					continue
				}
				if hasB {
					depHere := r.Time.Arrive(b, p)
					if b.Scheduled() {
						depHere = route.Trips[b.TripIdx].Dep[p]
					}
					if prev > depHere {
						continue
					}
					cand, ok := r.Time.Board(route, p, prev, q.Day)
					if ok && cand.Dep < depHere {
						b, bStop, bPos = cand, s, p
					}
					continue
				}
				if cand, ok := r.Time.Board(route, p, prev, q.Day); ok {
					b, hasB, bStop, bPos = cand, true, s, p
				}
			}
		}
		r.relaxFootpaths(k, tau, par, best, marked, &markedList, egress, &bestTarget)
		if len(markedList) == 0 {
			break
		}
	}

	return r.collect(q, tau, par, egress, rounds)
}

func (r *Router) relaxFootpaths(k int, tau [][]domain.Seconds, par [][]parent, best []domain.Seconds,
	marked []bool, markedList *[]domain.StopID, egress map[domain.StopID]domain.Seconds, bestTarget *domain.Seconds) {
	// Iterate over a snapshot; footpaths are single-hop.
	snapshot := append([]domain.StopID(nil), (*markedList)...)
	for _, s := range snapshot {
		if par[k][s].kind == parentWalk {
			continue // no walk-after-walk chains
		}
		base := tau[k][s]
		for _, f := range r.Net.FootpathsFrom(s) {
			t := base + domain.Seconds(f.Seconds)
			if t < tau[k][f.To] && t < best[f.To] {
				tau[k][f.To] = t
				best[f.To] = t
				par[k][f.To] = parent{kind: parentWalk, from: s, walk: domain.Seconds(f.Seconds)}
				if !marked[f.To] {
					marked[f.To] = true
					*markedList = append(*markedList, f.To)
				}
				if e, ok := egress[f.To]; ok && t+e < *bestTarget {
					*bestTarget = t + e
				}
			}
		}
	}
}

func (r *Router) collect(q Query, tau [][]domain.Seconds, par [][]parent, egress map[domain.StopID]domain.Seconds, rounds int) []Journey {
	var out []Journey
	prevBest := inf
	for k := 0; k < rounds; k++ {
		bestArr := inf
		var bestStop domain.StopID
		for stop, e := range egress {
			if tau[k][stop] == inf {
				continue
			}
			if a := tau[k][stop] + e; a < bestArr {
				bestArr, bestStop = a, stop
			}
		}
		if bestArr == inf || bestArr >= prevBest {
			continue
		}
		prevBest = bestArr
		j := r.reconstruct(k, bestStop, tau, par)
		if j == nil {
			continue
		}
		j.Arrive = bestArr
		j.Legs = append(j.Legs, Leg{Kind: LegWalk, From: bestStop, To: bestStop, Dep: tau[k][bestStop], Arr: bestArr})
		out = append(out, *j)
	}
	return out
}

// reconstruct walks parent pointers back from stop at round k.
func (r *Router) reconstruct(k int, stop domain.StopID, tau [][]domain.Seconds, par [][]parent) *Journey {
	var legs []Leg
	rides := 0
	for guard := 0; guard < 64; guard++ {
		p := par[k][stop]
		switch p.kind {
		case parentAccess:
			legs = append(legs, Leg{Kind: LegWalk, From: stop, To: stop, Dep: tau[k][stop] - p.walk, Arr: tau[k][stop]})
			// Reverse into forward order.
			for i, j := 0, len(legs)-1; i < j; i, j = i+1, j-1 {
				legs[i], legs[j] = legs[j], legs[i]
			}
			return &Journey{Legs: legs, Depart: legs[0].Dep, Rides: rides}
		case parentWalk:
			legs = append(legs, Leg{Kind: LegWalk, From: p.from, To: stop, Dep: tau[k][stop] - p.walk, Arr: tau[k][stop]})
			stop = p.from
		case parentRide:
			legs = append(legs, Leg{
				Kind: LegRide, Route: p.boarding.Route.ID, Boarding: p.boarding,
				FromPos: p.fromPos, ToPos: p.toPos, From: p.from, To: stop,
				Dep: p.boarding.Dep, Arr: tau[k][stop],
			})
			rides++
			stop = p.from
			k--
		default:
			return nil
		}
		if k < 0 {
			return nil
		}
	}
	return nil
}

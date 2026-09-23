package bmtc

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// FareStore persists fare samples and derived stage boundaries.
type FareStore interface {
	SaveFareSamples(ctx context.Context, routeSourceID string, samples []FareSampleRec) error
	SaveStageBoundaries(ctx context.Context, dataset, routeSourceID string, starts []int) error
	FareCursor(ctx context.Context) (string, error)
	SetFareCursor(ctx context.Context, v string) error
}

// FareSampleRec mirrors sqlite.FareSample without importing it.
type FareSampleRec struct {
	FromCode, ToCode string
	ServiceTypeID    int
	FarePaise        int32
}

// Shard selects every n-th route so the weekly job can be split across
// several CI runs: Index in [0, Count).
type Shard struct{ Index, Count int }

func (s Shard) owns(i int) bool {
	if s.Count <= 1 {
		return true
	}
	return i%s.Count == s.Index
}

// ScrapeFares samples fares from each route's first stop to every other
// stop and derives fare stage boundaries. It resumes from the stored
// cursor (the last completed route source id) unless resume is false.
func (s *Scraper) ScrapeFares(ctx context.Context, routes []domain.Route, stops []domain.Stop, serviceTypes []ServiceType, store FareStore, shard Shard, resume bool) (*Progress, error) {
	prog := &Progress{}
	typeID := map[string]int{}
	for _, st := range serviceTypes {
		typeID[strings.ToLower(st.Name)] = st.ID
	}
	sorted := make([]domain.Route, len(routes))
	copy(sorted, routes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SourceID < sorted[j].SourceID })

	cursor := ""
	if resume {
		cursor, _ = store.FareCursor(ctx)
	}
	for i, r := range sorted {
		if !shard.owns(i) || r.Mode != domain.ModeBus || len(r.Stops) < 2 {
			continue
		}
		if cursor != "" && r.SourceID <= cursor {
			continue
		}
		if err := ctx.Err(); err != nil {
			return prog, err
		}
		if err := s.fareRoute(ctx, r, stops, typeID, store, prog); err != nil {
			prog.Errors++
			s.Log.Warn("fare route failed", "route", r.ShortName, "source", r.SourceID, "err", err)
		}
		if err := store.SetFareCursor(ctx, r.SourceID); err != nil {
			return prog, err
		}
		prog.Routes++
		if prog.Routes%50 == 0 {
			s.Log.Info("fare progress", "routes", prog.Routes, "calls", s.Client.Calls, "errors", prog.Errors)
		}
	}
	prog.Calls = s.Client.Calls
	// A completed pass resets the cursor so next week starts over.
	if err := store.SetFareCursor(ctx, ""); err != nil {
		return prog, err
	}
	return prog, nil
}

// apiRouteID recovers the direction routeid from SourceID "<routeid>/<DIR>".
func apiRouteID(sourceID string) (int, string) {
	parts := strings.SplitN(sourceID, "/", 2)
	id, _ := strconv.Atoi(parts[0])
	dir := "UP"
	if len(parts) == 2 {
		dir = parts[1]
	}
	return id, dir
}

func stationID(stop domain.Stop) int {
	id, _ := strconv.Atoi(strings.TrimPrefix(stop.SourceID, "bus:"))
	return id
}

func (s *Scraper) fareRoute(ctx context.Context, r domain.Route, stops []domain.Stop, typeID map[string]int, store FareStore, prog *Progress) error {
	routeID, dir := apiRouteID(r.SourceID)
	direction := "Up"
	if dir == "DOWN" {
		direction = "Down"
	}
	from := stationID(stops[r.Stops[0]])
	var samples []FareSampleRec
	// Fare from the first stop to each position; index 0 is the origin.
	fares := make([]int32, len(r.Stops))
	known := make([]bool, len(r.Stops))
	misses := 0
	for pos := 1; pos < len(r.Stops); pos++ {
		to := stationID(stops[r.Stops[pos]])
		var fr []FareRoute
		if err := s.Client.Call(ctx, "GetFareRoutes", map[string]any{"fromStationId": from, "toStationId": to, "lan": "English"}, &fr); err != nil {
			misses++
			continue
		}
		var match *FareRoute
		for i := range fr {
			if fr[i].RouteID == routeID {
				match = &fr[i]
				break
			}
		}
		if match == nil {
			misses++
			continue
		}
		var items []FareItem
		err := s.Client.Call(ctx, "GetMobileFareData_v2", map[string]any{
			"routeno": r.ShortName, "routeid": routeID, "route_direction": direction,
			"source_code": match.SourceCode, "destination_code": match.DestinationCode,
		}, &items)
		if err != nil {
			misses++
			continue
		}
		var primary int32 = -1
		for _, it := range items {
			paise := int32(float64(it.Fare)*100 + 0.5)
			samples = append(samples, FareSampleRec{FromCode: match.SourceCode, ToCode: match.DestinationCode, ServiceTypeID: typeID[strings.ToLower(it.ServiceType)], FarePaise: paise})
			// The class chart we price on: the cheapest listed fare is the
			// ordinary one for ordinary routes; AC routes list only AC.
			if primary < 0 || paise < primary {
				primary = paise
			}
		}
		if primary >= 0 {
			fares[pos], known[pos] = primary, true
		}
	}
	if len(samples) > 0 {
		if err := store.SaveFareSamples(ctx, r.SourceID, samples); err != nil {
			return err
		}
	}
	starts := stageStarts(fares, known)
	if len(known) > 2 && countTrue(known) >= (len(known)-1)/2 {
		if err := store.SaveStageBoundaries(ctx, "bmtc", r.SourceID, starts); err != nil {
			return err
		}
	} else if misses > 0 {
		return fmt.Errorf("%d of %d stop pairs unresolved", misses, len(r.Stops)-1)
	}
	return nil
}

// stageStarts returns positions where the fare from the origin increases,
// i.e. where a new fare stage begins. Unknown positions are skipped.
func stageStarts(fares []int32, known []bool) []int {
	var starts []int
	last := int32(-1)
	for pos := 1; pos < len(fares); pos++ {
		if !known[pos] {
			continue
		}
		if last >= 0 && fares[pos] > last {
			starts = append(starts, pos)
		}
		last = fares[pos]
	}
	return starts
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

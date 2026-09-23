package bmtc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/geo"
)

// Dataset is the scraped network; it satisfies network.Source and is what
// the store persists.
type Dataset struct {
	version   string
	stops     []domain.Stop
	routes    []domain.Route
	stopIdx   map[int]domain.StopID
	ServiceTs []ServiceType
}

func (d *Dataset) Name() string                 { return "bmtc" }
func (d *Dataset) Source() string               { return "scrape" }
func (d *Dataset) Version() string              { return d.version }
func (d *Dataset) Stops() []domain.Stop         { return d.stops }
func (d *Dataset) Routes() []domain.Route       { return d.routes }
func (d *Dataset) Footpaths() []domain.Footpath { return nil }

// Progress is reported to the run log.
type Progress struct {
	Calls, Errors, Routes, Stops, Trips int
	Partial                             bool
	Notes                               []string
}

// Scraper runs the jobs.
type Scraper struct {
	Client *Client
	Log    *slog.Logger
	// MaxErrorRate aborts a run as "partial" when exceeded. Default 0.05.
	MaxErrorRate float64
	// MaxShapeErrors stops a job after this many consecutive shape errors,
	// which indicates the API changed. Default 3.
	MaxShapeErrors int
	// Only limits the core job to route numbers with these prefixes (for
	// tests and shards); empty means all.
	Only []string
}

// ScrapeCore runs jobs 1-4: service types, routes, stops+shape distances,
// timetables. It returns the dataset ready for Store.ReplaceDataset.
func (s *Scraper) ScrapeCore(ctx context.Context) (*Dataset, *Progress, error) {
	if s.MaxErrorRate == 0 {
		s.MaxErrorRate = 0.05
	}
	if s.MaxShapeErrors == 0 {
		s.MaxShapeErrors = 3
	}
	prog := &Progress{}
	ds := &Dataset{version: time.Now().UTC().Format("20060102"), stopIdx: map[int]domain.StopID{}}

	if err := s.Client.Call(ctx, "GetAllServiceTypes", map[string]any{}, &ds.ServiceTs); err != nil {
		return nil, prog, fmt.Errorf("service types: %w", err)
	}
	var list []RouteListItem
	if err := s.Client.Call(ctx, "GetAllRouteList", map[string]any{}, &list); err != nil {
		return nil, prog, fmt.Errorf("route list: %w", err)
	}
	// Group directions by base route number.
	byBase := map[string][]RouteListItem{}
	for _, r := range list {
		if len(s.Only) > 0 && !hasPrefix(r.Base(), s.Only) {
			continue
		}
		byBase[r.Base()] = append(byBase[r.Base()], r)
	}
	bases := make([]string, 0, len(byBase))
	for b := range byBase {
		bases = append(bases, b)
	}
	sort.Strings(bases)
	s.Log.Info("route list", "directions", len(list), "routes", len(bases))

	shapeErrs := 0
	attempts := 0
	for i, base := range bases {
		attempts++
		err := s.scrapeRoute(ctx, ds, base, byBase[base], prog)
		if err != nil {
			prog.Errors++
			if errors.Is(err, ErrShape) {
				shapeErrs++
				if shapeErrs >= s.MaxShapeErrors {
					prog.Partial = true
					prog.Notes = append(prog.Notes, "aborted: repeated shape errors, API may have changed")
					break
				}
			} else {
				shapeErrs = 0
			}
			s.Log.Warn("route failed", "route", base, "err", err)
			if attempts >= 20 && float64(prog.Errors)/float64(attempts) > s.MaxErrorRate {
				prog.Partial = true
				prog.Notes = append(prog.Notes, fmt.Sprintf("aborted after %d routes: error rate %.0f%%", attempts, 100*float64(prog.Errors)/float64(attempts)))
				break
			}
			continue
		}
		shapeErrs = 0
		if (i+1)%100 == 0 {
			s.Log.Info("progress", "routes", i+1, "of", len(bases), "calls", s.Client.Calls, "errors", prog.Errors)
		}
	}
	prog.Calls = s.Client.Calls
	prog.Routes = len(ds.routes)
	prog.Stops = len(ds.stops)
	for _, r := range ds.routes {
		prog.Trips += len(r.Trips)
	}
	return ds, prog, nil
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// scrapeRoute fetches stops for both directions of one route number and
// its timetables.
func (s *Scraper) scrapeRoute(ctx context.Context, ds *Dataset, base string, dirs []RouteListItem, prog *Progress) error {
	var found []SearchRouteItem
	if err := s.Client.Call(ctx, "SearchRoute_v2", map[string]any{"routetext": base}, &found); err != nil {
		return err
	}
	parent := 0
	for _, f := range found {
		if strings.EqualFold(strings.TrimSpace(f.RouteNo), base) {
			parent = f.RouteParentID
			break
		}
	}
	if parent == 0 {
		return fmt.Errorf("no exact parent for %q among %d results", base, len(found))
	}
	raw, err := s.Client.Post(ctx, "SearchByRouteDetails_v4", map[string]any{"routeid": parent, "servicetypeid": 0})
	if err != nil {
		return err
	}
	var det RouteDetails
	if err := decodeJSON(raw, &det); err != nil {
		return fmt.Errorf("route details: %w", err)
	}
	if len(det.Up.Data) < 2 && len(det.Down.Data) < 2 {
		return fmt.Errorf("route details: %w: no stops", ErrShape)
	}
	for _, dir := range []struct {
		name string
		data []RouteStop
	}{{"UP", det.Up.Data}, {"DOWN", det.Down.Data}} {
		if len(dir.data) < 2 {
			continue
		}
		item, ok := matchDirection(dirs, dir.name, dir.data)
		if !ok {
			continue
		}
		route := ds.addRoute(base, dir.name, item, dir.data)
		if err := s.scrapeTimetable(ctx, ds, route, item, dir.data); err != nil {
			// Timetable failures are common (some routes have none); keep
			// the route with a default headway rather than dropping it.
			s.Log.Debug("timetable", "route", item.RouteNo, "err", err)
			prog.Notes = appendOnce(prog.Notes, "some routes have no timetable; default headway used")
		}
	}
	return nil
}

// matchDirection finds the RouteListItem for a direction. The list's
// routeid per direction is what timetables key on.
func matchDirection(dirs []RouteListItem, name string, stops []RouteStop) (RouteListItem, bool) {
	for _, d := range dirs {
		if d.Direction() == name {
			return d, true
		}
	}
	// Fall back to matching by first stop id.
	for _, d := range dirs {
		if d.FromStationID == stops[0].StationID {
			return d, true
		}
	}
	return RouteListItem{}, false
}

func (ds *Dataset) stopFor(rs RouteStop) domain.StopID {
	if id, ok := ds.stopIdx[rs.StationID]; ok {
		return id
	}
	id := domain.StopID(len(ds.stops))
	ds.stops = append(ds.stops, domain.Stop{
		ID:       id,
		SourceID: "bus:" + strconv.Itoa(rs.StationID),
		Name:     strings.TrimSpace(rs.StationName),
		Loc:      domain.LatLng{Lat: float64(rs.Lat), Lng: float64(rs.Lng)},
		Kind:     domain.StopBus,
	})
	ds.stopIdx[rs.StationID] = id
	return id
}

func (ds *Dataset) addRoute(base, dir string, item RouteListItem, stops []RouteStop) *domain.Route {
	r := domain.Route{
		ID:        domain.RouteID(len(ds.routes)),
		SourceID:  fmt.Sprintf("%d/%s", item.RouteID, dir),
		ShortName: base,
		Headsign:  strings.TrimSpace(item.ToStation),
		Mode:      domain.ModeBus,
		Class:     domain.ClassFromRouteNumber(base),
	}
	if r.Headsign == "" {
		r.Headsign = strings.TrimSpace(stops[len(stops)-1].StationName)
	}
	r.Stops = make([]domain.StopID, len(stops))
	r.DistKm = make([]float64, len(stops))
	useAPI := true
	for i, st := range stops {
		r.Stops[i] = ds.stopFor(st)
		r.DistKm[i] = float64(st.DistanceKm)
		if i > 0 && r.DistKm[i] < r.DistKm[i-1] {
			useAPI = false
		}
	}
	if !useAPI || r.DistKm[len(r.DistKm)-1] <= 0 {
		// distance_on_station missing or non-monotonic: chain haversine.
		for i := range r.DistKm {
			if i == 0 {
				r.DistKm[i] = 0
				continue
			}
			r.DistKm[i] = r.DistKm[i-1] + geo.DistanceM(ds.stops[r.Stops[i-1]].Loc, ds.stops[r.Stops[i]].Loc)/1000
		}
	}
	ds.routes = append(ds.routes, r)
	return &ds.routes[len(ds.routes)-1]
}

// scrapeTimetable turns the API's per-trip start/end times into explicit
// trips with stop times interpolated by distance.
func (s *Scraper) scrapeTimetable(ctx context.Context, ds *Dataset, route *domain.Route, item RouteListItem, stops []RouteStop) error {
	today := time.Now().In(time.FixedZone("IST", 19800))
	day := today.Format("2006-01-02")
	var items []TimetableItem
	err := s.Client.Call(ctx, "GetTimetableByRouteid_v3", map[string]any{
		"routeid":       item.RouteID,
		"fromStationId": stops[0].StationID,
		"toStationId":   stops[len(stops)-1].StationID,
		"starttime":     day + " 00:00",
		"endtime":       day + " 23:59",
		"current_date":  today.UTC().Format("2006-01-02T15:04:05.000Z"),
	}, &items)
	if err != nil {
		return err
	}
	all := domain.Weekday.Bit() | domain.Saturday.Bit() | domain.Sunday.Bit()
	total := route.DistKm[len(route.DistKm)-1]
	for _, it := range items {
		for _, tt := range it.Trips {
			start, ok1 := parseHHMM(tt.Start)
			end, ok2 := parseHHMM(tt.End)
			if !ok1 || !ok2 {
				continue
			}
			if end < start {
				end += 86400
			}
			n := len(route.Stops)
			trip := domain.Trip{Days: all, Arr: make([]domain.Seconds, n), Dep: make([]domain.Seconds, n)}
			for i := 0; i < n; i++ {
				frac := float64(i) / float64(n-1)
				if total > 0 {
					frac = route.DistKm[i] / total
				}
				t := start + domain.Seconds(float64(end-start)*frac)
				trip.Arr[i], trip.Dep[i] = t, t
			}
			route.Trips = append(route.Trips, trip)
		}
	}
	sort.Slice(route.Trips, func(i, j int) bool { return route.Trips[i].Dep[0] < route.Trips[j].Dep[0] })
	return nil
}

func parseHHMM(s string) (domain.Seconds, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return domain.Seconds(h*3600 + m*60), true
}

func appendOnce(notes []string, n string) []string {
	for _, x := range notes {
		if x == n {
			return notes
		}
	}
	return append(notes, n)
}

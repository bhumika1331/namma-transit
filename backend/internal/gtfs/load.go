// Package gtfs loads a GTFS static zip into the domain model. It is used for
// the community Vonter feeds (bmtc-gtfs, bmrcl-gtfs) that bootstrap the app
// before our own scraper has data, and as a permanent fallback.
package gtfs

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/geo"
)

// Options controls how a feed is interpreted.
type Options struct {
	// Name identifies the dataset for Health ("bmtc", "bmrcl").
	Name string
	// InterchangeSeconds is the walking penalty between two lines' platforms
	// at the same metro station. Default 360.
	InterchangeSeconds int32
}

// Dataset is a loaded feed. It satisfies network.Source.
type Dataset struct {
	name      string
	version   string
	stops     []domain.Stop
	routes    []domain.Route
	footpaths []domain.Footpath
}

func (d *Dataset) Name() string                 { return d.name }
func (d *Dataset) Source() string               { return "gtfs" }
func (d *Dataset) Version() string              { return d.version }
func (d *Dataset) Stops() []domain.Stop         { return d.stops }
func (d *Dataset) Routes() []domain.Route       { return d.routes }
func (d *Dataset) Footpaths() []domain.Footpath { return d.footpaths }

type gStop struct {
	name, parent, desc string
	loc                domain.LatLng
	isStation          bool
}

type gRoute struct {
	short, long, color string
	rtype              int
}

type gTrip struct {
	route, service, headsign string
	dir                      byte
}

type event struct {
	seq      int32
	stop     string
	arr, dep domain.Seconds
	dist     float64
	hasDist  bool
}

// Representative day used to decide which services run on each DayKind.
var repDay = map[domain.DayKind]time.Weekday{
	domain.Weekday:  time.Wednesday,
	domain.Saturday: time.Saturday,
	domain.Sunday:   time.Sunday,
}

// Load parses the zip at path.
func Load(path string, opts Options) (*Dataset, error) {
	if opts.InterchangeSeconds == 0 {
		opts.InterchangeSeconds = 360
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()

	ds := &Dataset{name: opts.Name}
	if st, err := os.Stat(path); err == nil {
		ds.version = st.ModTime().UTC().Format("20060102T150405Z")
	}
	_ = forEachRow(z, "feed_info.txt", func(r row) error {
		if v := r.get("feed_version"); v != "" {
			ds.version = v
		}
		return nil
	})

	stops := map[string]gStop{}
	if err := forEachRow(z, "stops.txt", func(r row) error {
		lat, ok1 := parseFloat(r.get("stop_lat"))
		lng, ok2 := parseFloat(r.get("stop_lon"))
		if !ok1 || !ok2 {
			return nil // station entries without coords are skipped
		}
		stops[r.get("stop_id")] = gStop{
			name:      strings.TrimSpace(r.get("stop_name")),
			parent:    r.get("parent_station"),
			desc:      strings.TrimSpace(r.get("stop_desc")),
			loc:       domain.LatLng{Lat: lat, Lng: lng},
			isStation: r.get("location_type") == "1",
		}
		return nil
	}); err != nil {
		return nil, err
	}

	routes := map[string]gRoute{}
	if err := forEachRow(z, "routes.txt", func(r row) error {
		rt := 3
		if v, ok := parseFloat(r.get("route_type")); ok {
			rt = int(v)
		}
		routes[r.get("route_id")] = gRoute{
			short: strings.TrimSpace(r.get("route_short_name")),
			long:  strings.TrimSpace(r.get("route_long_name")),
			color: strings.TrimSpace(r.get("route_color")),
			rtype: rt,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// service_id -> which DayKinds it serves.
	services := map[string]domain.DayMask{}
	err = forEachRow(z, "calendar.txt", func(r row) error {
		var mask domain.DayMask
		for dk, wd := range repDay {
			col := strings.ToLower(wd.String())
			if r.get(col) == "1" {
				mask |= dk.Bit()
			}
		}
		services[r.get("service_id")] = mask
		return nil
	})
	if err != nil && !errors.Is(err, errNoFile) {
		return nil, err
	}

	trips := map[string]gTrip{}
	if err := forEachRow(z, "trips.txt", func(r row) error {
		var dir byte
		if r.get("direction_id") == "1" {
			dir = 1
		}
		trips[r.get("trip_id")] = gTrip{
			route:    r.get("route_id"),
			service:  r.get("service_id"),
			headsign: strings.TrimSpace(r.get("trip_headsign")),
			dir:      dir,
		}
		return nil
	}); err != nil {
		return nil, err
	}

	events := make(map[string][]event, len(trips))
	if err := forEachRow(z, "stop_times.txt", func(r row) error {
		tid := r.get("trip_id")
		if _, ok := trips[tid]; !ok {
			return nil
		}
		seq, _ := parseFloat(r.get("stop_sequence"))
		arr, err1 := parseTime(r.get("arrival_time"))
		dep, err2 := parseTime(r.get("departure_time"))
		if err1 != nil && err2 != nil {
			return nil // untimed intermediate stop; interpolation is out of scope
		}
		if err1 != nil {
			arr = dep
		}
		if err2 != nil {
			dep = arr
		}
		ev := event{seq: int32(seq), stop: r.get("stop_id"), arr: arr, dep: dep}
		if r.has("shape_dist_traveled") {
			ev.dist, ev.hasDist = parseFloat(r.get("shape_dist_traveled"))
		}
		events[tid] = append(events[tid], ev)
		return nil
	}); err != nil {
		return nil, err
	}

	b := &builder{
		ds:      ds,
		stops:   stops,
		routes:  routes,
		stopIdx: map[string]domain.StopID{},
		patIdx:  map[string]int{},
		patSeq:  map[string]int{},
	}
	// Deterministic order: iterate trips sorted by id.
	tripIDs := make([]string, 0, len(events))
	for id := range events {
		tripIDs = append(tripIDs, id)
	}
	sort.Strings(tripIDs)
	for _, tid := range tripIDs {
		t := trips[tid]
		mask, ok := services[t.service]
		if !ok {
			// No calendar: assume daily service (bmtc feed has one all-days service).
			mask = domain.Weekday.Bit() | domain.Saturday.Bit() | domain.Sunday.Bit()
		}
		if mask == 0 {
			continue
		}
		if err := b.addTrip(t, mask, events[tid]); err != nil {
			return nil, fmt.Errorf("trip %s: %w", tid, err)
		}
	}
	b.finish(opts.InterchangeSeconds)
	return ds, nil
}

type builder struct {
	ds      *Dataset
	stops   map[string]gStop
	routes  map[string]gRoute
	stopIdx map[string]domain.StopID // stop key -> domain id
	patIdx  map[string]int           // pattern key -> index into ds.routes
	patSeq  map[string]int           // "route/dir" -> patterns seen, for unique SourceIDs
	// metro: station parent -> stops on each line, for interchange footpaths
	station map[string][]domain.StopID
}

// stopKey decides the routing-level identity of a GTFS stop. Metro platforms
// collapse to one stop per (line, station); bus stops keep their own id.
func (b *builder) stopKey(gr gRoute, routeID, gtfsStop string) (key string, station string) {
	if gr.rtype != 3 {
		st := gtfsStop
		if p := b.stops[gtfsStop].parent; p != "" {
			st = p
		}
		return routeID + "|" + st, st
	}
	return gtfsStop, ""
}

func (b *builder) stopFor(gr gRoute, routeID, gtfsStop string) (domain.StopID, error) {
	key, station := b.stopKey(gr, routeID, gtfsStop)
	if id, ok := b.stopIdx[key]; ok {
		return id, nil
	}
	gs, ok := b.stops[gtfsStop]
	if !ok {
		return 0, fmt.Errorf("unknown stop %q", gtfsStop)
	}
	id := domain.StopID(len(b.ds.stops))
	s := domain.Stop{ID: id, Name: gs.name, Loc: gs.loc}
	if gr.rtype == 3 {
		s.Kind = domain.StopBus
		s.SourceID = "bus:" + gtfsStop
		s.SubLabel = gs.desc
	} else {
		s.Kind = domain.StopMetro
		s.SourceID = "metro:" + routeID + ":" + station
		s.SubLabel = lineName(gr)
		if ps, ok := b.stops[station]; ok {
			s.Name = ps.name
			s.Loc = ps.loc
		}
		if b.station == nil {
			b.station = map[string][]domain.StopID{}
		}
		b.station[station] = append(b.station[station], id)
	}
	b.ds.stops = append(b.ds.stops, s)
	b.stopIdx[key] = id
	return id, nil
}

func lineName(gr gRoute) string {
	if gr.short == "" {
		return gr.long
	}
	if strings.HasSuffix(strings.ToLower(gr.short), "line") {
		return gr.short
	}
	return gr.short + " Line"
}

func (b *builder) addTrip(t gTrip, days domain.DayMask, evs []event) error {
	gr, ok := b.routes[t.route]
	if !ok {
		return fmt.Errorf("unknown route %q", t.route)
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].seq < evs[j].seq })
	if len(evs) < 2 {
		return nil
	}
	ids := make([]domain.StopID, len(evs))
	var key strings.Builder
	key.WriteString(t.route)
	key.WriteByte('|')
	key.WriteByte('0' + t.dir)
	for i, ev := range evs {
		id, err := b.stopFor(gr, t.route, ev.stop)
		if err != nil {
			return err
		}
		ids[i] = id
		fmt.Fprintf(&key, "|%d", id)
	}

	pi, ok := b.patIdx[key.String()]
	if !ok {
		pi = len(b.ds.routes)
		b.patIdx[key.String()] = pi
		base := t.route + "/" + string('0'+t.dir)
		b.patSeq[base]++
		sourceID := base
		if n := b.patSeq[base]; n > 1 {
			sourceID = fmt.Sprintf("%s#%d", base, n)
		}
		r := domain.Route{
			ID:        domain.RouteID(pi),
			SourceID:  sourceID,
			ShortName: gr.short,
			Headsign:  t.headsign,
			Stops:     ids,
			DistKm:    distances(b.ds.stops, ids, evs),
		}
		if gr.rtype == 3 {
			r.Mode = domain.ModeBus
			r.Class = ClassFromRouteNumber(gr.short)
		} else {
			r.Mode = domain.ModeMetroRail
			r.Class = domain.ClassMetro
			r.ShortName = lineName(gr)
			r.LineColor = gr.color
		}
		if r.Headsign == "" {
			r.Headsign = b.ds.stops[ids[len(ids)-1]].Name
		}
		b.ds.routes = append(b.ds.routes, r)
	}
	arr := make([]domain.Seconds, len(evs))
	dep := make([]domain.Seconds, len(evs))
	for i, ev := range evs {
		arr[i], dep[i] = ev.arr, ev.dep
	}
	b.ds.routes[pi].Trips = append(b.ds.routes[pi].Trips, domain.Trip{Days: days, Arr: arr, Dep: dep})
	return nil
}

// distances returns cumulative km along the pattern, preferring the feed's
// shape_dist_traveled when present and monotonic, else chained haversine.
func distances(stops []domain.Stop, ids []domain.StopID, evs []event) []float64 {
	out := make([]float64, len(ids))
	useShape := true
	for i, ev := range evs {
		if !ev.hasDist || (i > 0 && ev.dist < evs[i-1].dist) {
			useShape = false
			break
		}
	}
	if useShape && evs[len(evs)-1].dist > 0 {
		base := evs[0].dist
		for i, ev := range evs {
			out[i] = ev.dist - base
		}
		// Feeds may use metres; normalise when the total is implausibly large.
		if out[len(out)-1] > 500 {
			for i := range out {
				out[i] /= 1000
			}
		}
		return out
	}
	for i := 1; i < len(ids); i++ {
		out[i] = out[i-1] + geo.DistanceM(stops[ids[i-1]].Loc, stops[ids[i]].Loc)/1000
	}
	return out
}

func (b *builder) finish(interchange int32) {
	for _, r := range b.ds.routes {
		sort.Slice(r.Trips, func(i, j int) bool { return r.Trips[i].Dep[0] < r.Trips[j].Dep[0] })
	}
	// Interchange footpaths between lines sharing a station.
	stations := make([]string, 0, len(b.station))
	for st := range b.station {
		stations = append(stations, st)
	}
	sort.Strings(stations)
	for _, st := range stations {
		ids := b.station[st]
		for i := range ids {
			for j := range ids {
				if i != j {
					b.ds.footpaths = append(b.ds.footpaths, domain.Footpath{From: ids[i], To: ids[j], Seconds: interchange})
				}
			}
		}
	}
}

// ClassFromRouteNumber is kept for callers; the logic lives in domain.
func ClassFromRouteNumber(short string) domain.ServiceClass {
	return domain.ClassFromRouteNumber(short)
}

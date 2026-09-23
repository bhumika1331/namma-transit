// Package sqlite persists the transit network and scraper output in a single
// SQLite file using the pure-Go modernc driver. It implements network.Source
// so the server can run from scraped data exactly as it runs from GTFS.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
)

//go:embed schema.sql
var schema string

// Store wraps one database file.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database and applies the schema.
// readOnly opens with SQLite's read-only mode for the server.
func Open(path string, readOnly bool) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	if readOnly {
		dsn += "&mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if !readOnly {
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply schema: %w", err)
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for scraper repositories in this package's siblings.
func (s *Store) DB() *sql.DB { return s.db }

// SetMeta / GetMeta store small key-values (data_version, fare cursor).
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// ReplaceDataset atomically swaps one dataset's stops, routes, trips and
// footpaths with the given source. IDs in src are local; they are mapped to
// database ids here and back to dense local ids on load.
func (s *Store) ReplaceDataset(ctx context.Context, src network.Source) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ds := src.Name()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`DELETE FROM footpaths WHERE from_stop IN (SELECT id FROM stops WHERE dataset=?)`,
		`DELETE FROM trips WHERE route_id IN (SELECT id FROM routes WHERE dataset=?)`,
		`DELETE FROM headways WHERE route_id IN (SELECT id FROM routes WHERE dataset=?)`,
		`DELETE FROM route_stops WHERE route_id IN (SELECT id FROM routes WHERE dataset=?)`,
		`DELETE FROM fare_stage_boundaries WHERE route_id IN (SELECT id FROM routes WHERE dataset=?)`,
		`DELETE FROM routes WHERE dataset=?`,
		`DELETE FROM stops WHERE dataset=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, ds); err != nil {
			return err
		}
	}

	stopIDs := make(map[domain.StopID]int64, len(src.Stops()))
	insStop, err := tx.PrepareContext(ctx, `INSERT INTO stops(dataset,source,source_id,name,lat,lng,kind,sub_label,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for _, st := range src.Stops() {
		res, err := insStop.ExecContext(ctx, ds, src.Source(), st.SourceID, st.Name, st.Loc.Lat, st.Loc.Lng, st.Kind, st.SubLabel, now)
		if err != nil {
			return fmt.Errorf("stop %s: %w", st.SourceID, err)
		}
		id, _ := res.LastInsertId()
		stopIDs[st.ID] = id
	}

	insRoute, err := tx.PrepareContext(ctx, `INSERT INTO routes(dataset,source,source_id,short_name,headsign,mode,service_class,line_color,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	insRS, err := tx.PrepareContext(ctx, `INSERT INTO route_stops(route_id,seq,stop_id,dist_km) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	insTrip, err := tx.PrepareContext(ctx, `INSERT INTO trips(route_id,days,dep0,arr,dep) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	insHW, err := tx.PrepareContext(ctx, `INSERT INTO headways(route_id,day,from_sec,to_sec,every_sec) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for _, r := range src.Routes() {
		res, err := insRoute.ExecContext(ctx, ds, src.Source(), r.SourceID, r.ShortName, r.Headsign, r.Mode, r.Class, r.LineColor, now)
		if err != nil {
			return fmt.Errorf("route %s: %w", r.SourceID, err)
		}
		rid, _ := res.LastInsertId()
		for i, st := range r.Stops {
			var km float64
			if i < len(r.DistKm) {
				km = r.DistKm[i]
			}
			if _, err := insRS.ExecContext(ctx, rid, i, stopIDs[st], km); err != nil {
				return err
			}
		}
		for _, t := range r.Trips {
			if _, err := insTrip.ExecContext(ctx, rid, t.Days, t.Dep[0], pack(t.Arr), pack(t.Dep)); err != nil {
				return err
			}
		}
		for _, h := range r.Headways {
			if _, err := insHW.ExecContext(ctx, rid, h.Day, h.From, h.To, h.Every); err != nil {
				return err
			}
		}
	}
	insFP, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO footpaths(from_stop,to_stop,seconds) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	for _, f := range src.Footpaths() {
		if _, err := insFP.ExecContext(ctx, stopIDs[f.From], stopIDs[f.To], f.Seconds); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "version:"+ds, src.Version()); err != nil {
		return err
	}
	return tx.Commit()
}

func pack(v []domain.Seconds) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(x))
	}
	return b
}

func unpack(b []byte) []domain.Seconds {
	out := make([]domain.Seconds, len(b)/4)
	for i := range out {
		out[i] = domain.Seconds(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

// Dataset is a loaded snapshot of one dataset; it satisfies network.Source.
type Dataset struct {
	name, source, version string
	stops                 []domain.Stop
	routes                []domain.Route
	footpaths             []domain.Footpath
}

func (d *Dataset) Name() string                 { return d.name }
func (d *Dataset) Source() string               { return d.source }
func (d *Dataset) Version() string              { return d.version }
func (d *Dataset) Stops() []domain.Stop         { return d.stops }
func (d *Dataset) Routes() []domain.Route       { return d.routes }
func (d *Dataset) Footpaths() []domain.Footpath { return d.footpaths }

// Datasets lists dataset names present.
func (s *Store) Datasets(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT dataset FROM routes ORDER BY dataset`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LoadDataset reads one dataset into memory with dense local ids.
func (s *Store) LoadDataset(ctx context.Context, name string) (*Dataset, error) {
	d := &Dataset{name: name}
	d.version, _ = s.GetMeta(ctx, "version:"+name)

	dbToLocal := map[int64]domain.StopID{}
	rows, err := s.db.QueryContext(ctx, `SELECT id,source,source_id,name,lat,lng,kind,sub_label FROM stops WHERE dataset=? ORDER BY id`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var st domain.Stop
		if err := rows.Scan(&id, &d.source, &st.SourceID, &st.Name, &st.Loc.Lat, &st.Loc.Lng, &st.Kind, &st.SubLabel); err != nil {
			rows.Close()
			return nil, err
		}
		st.ID = domain.StopID(len(d.stops))
		dbToLocal[id] = st.ID
		d.stops = append(d.stops, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	routeIdx := map[int64]int{}
	rows, err = s.db.QueryContext(ctx, `SELECT id,source_id,short_name,headsign,mode,service_class,line_color FROM routes WHERE dataset=? ORDER BY id`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var r domain.Route
		if err := rows.Scan(&id, &r.SourceID, &r.ShortName, &r.Headsign, &r.Mode, &r.Class, &r.LineColor); err != nil {
			rows.Close()
			return nil, err
		}
		r.ID = domain.RouteID(len(d.routes))
		routeIdx[id] = len(d.routes)
		d.routes = append(d.routes, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT rs.route_id, rs.stop_id, rs.dist_km FROM route_stops rs JOIN routes r ON r.id=rs.route_id WHERE r.dataset=? ORDER BY rs.route_id, rs.seq`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rid, sid int64
		var km float64
		if err := rows.Scan(&rid, &sid, &km); err != nil {
			rows.Close()
			return nil, err
		}
		r := &d.routes[routeIdx[rid]]
		r.Stops = append(r.Stops, dbToLocal[sid])
		r.DistKm = append(r.DistKm, km)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT t.route_id, t.days, t.arr, t.dep FROM trips t JOIN routes r ON r.id=t.route_id WHERE r.dataset=? ORDER BY t.route_id, t.dep0`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rid int64
		var days domain.DayMask
		var arr, dep []byte
		if err := rows.Scan(&rid, &days, &arr, &dep); err != nil {
			rows.Close()
			return nil, err
		}
		r := &d.routes[routeIdx[rid]]
		r.Trips = append(r.Trips, domain.Trip{Days: days, Arr: unpack(arr), Dep: unpack(dep)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT h.route_id, h.day, h.from_sec, h.to_sec, h.every_sec FROM headways h JOIN routes r ON r.id=h.route_id WHERE r.dataset=? ORDER BY h.route_id, h.day, h.from_sec`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rid int64
		var h domain.Headway
		if err := rows.Scan(&rid, &h.Day, &h.From, &h.To, &h.Every); err != nil {
			rows.Close()
			return nil, err
		}
		r := &d.routes[routeIdx[rid]]
		r.Headways = append(r.Headways, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT f.from_stop, f.to_stop, f.seconds FROM footpaths f JOIN stops s ON s.id=f.from_stop WHERE s.dataset=?`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var from, to int64
		var sec int32
		if err := rows.Scan(&from, &to, &sec); err != nil {
			rows.Close()
			return nil, err
		}
		d.footpaths = append(d.footpaths, domain.Footpath{From: dbToLocal[from], To: dbToLocal[to], Seconds: sec})
	}
	rows.Close()
	return d, rows.Err()
}

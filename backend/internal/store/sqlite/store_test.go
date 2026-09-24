package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/gtfs"
)

type fake struct {
	stops []domain.Stop
	rts   []domain.Route
	fps   []domain.Footpath
}

func (f *fake) Name() string                 { return "toy" }
func (f *fake) Source() string               { return "test" }
func (f *fake) Version() string              { return "v7" }
func (f *fake) Stops() []domain.Stop         { return f.stops }
func (f *fake) Routes() []domain.Route       { return f.rts }
func (f *fake) Footpaths() []domain.Footpath { return f.fps }

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	src := &fake{
		stops: []domain.Stop{
			{ID: 0, SourceID: "bus:1", Name: "A", Loc: domain.LatLng{Lat: 12.9, Lng: 77.5}, Kind: domain.StopBus, SubLabel: "x"},
			{ID: 1, SourceID: "bus:2", Name: "B", Loc: domain.LatLng{Lat: 12.91, Lng: 77.5}, Kind: domain.StopBus},
		},
		rts: []domain.Route{{
			ID: 0, SourceID: "r/0", ShortName: "R1", Headsign: "B", Mode: domain.ModeBus, Class: domain.ClassVajra,
			Stops: []domain.StopID{0, 1}, DistKm: []float64{0, 1.5},
			Trips: []domain.Trip{
				{Days: 7, Arr: []domain.Seconds{100, 200}, Dep: []domain.Seconds{110, 200}},
				{Days: 1, Arr: []domain.Seconds{50, 150}, Dep: []domain.Seconds{60, 150}},
			},
			Headways: []domain.Headway{{Day: domain.Sunday, From: 100, To: 200, Every: 10}},
		}},
		fps: []domain.Footpath{{From: 0, To: 1, Seconds: 90}},
	}
	if err := st.ReplaceDataset(ctx, src); err != nil {
		t.Fatal(err)
	}
	// Replace again: must not duplicate.
	if err := st.ReplaceDataset(ctx, src); err != nil {
		t.Fatal(err)
	}
	names, err := st.Datasets(ctx)
	if err != nil || !reflect.DeepEqual(names, []string{"toy"}) {
		t.Fatalf("datasets %v %v", names, err)
	}
	ds, err := st.LoadDataset(ctx, "toy")
	if err != nil {
		t.Fatal(err)
	}
	if ds.Version() != "v7" || ds.Source() != "test" {
		t.Fatalf("version %q source %q", ds.Version(), ds.Source())
	}
	if !reflect.DeepEqual(ds.Stops(), src.stops) {
		t.Fatalf("stops\n got %+v\nwant %+v", ds.Stops(), src.stops)
	}
	got := ds.Routes()[0]
	want := src.rts[0]
	// Trips come back ordered by first departure.
	want.Trips = []domain.Trip{want.Trips[1], want.Trips[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("route\n got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(ds.Footpaths(), src.fps) {
		t.Fatalf("footpaths %+v", ds.Footpaths())
	}

	if err := st.Finalize(ctx); err != nil {
		t.Fatal(err)
	}
	// Read-only immutable handle works for the server path.
	ro, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if v, _ := ro.GetMeta(ctx, "version:toy"); v != "v7" {
		t.Fatalf("meta = %q", v)
	}
	if ds2, err := ro.LoadDataset(ctx, "toy"); err != nil || len(ds2.Routes()) != 1 {
		t.Fatalf("read-only load: %v", err)
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		t.Fatal("finalize should remove the WAL file")
	}
}

func TestMetroFeedThroughSQLite(t *testing.T) {
	const zip = "../../../data/gtfs/bmrcl.zip"
	if _, err := os.Stat(zip); err != nil {
		t.Skip("feed missing")
	}
	ctx := context.Background()
	src, err := gtfs.Load(zip, gtfs.Options{Name: "bmrcl"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(filepath.Join(t.TempDir(), "m.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReplaceDataset(ctx, src); err != nil {
		t.Fatal(err)
	}
	ds, err := st.LoadDataset(ctx, "bmrcl")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Stops()) != len(src.Stops()) || len(ds.Routes()) != len(src.Routes()) || len(ds.Footpaths()) != len(src.Footpaths()) {
		t.Fatalf("counts differ: %d/%d stops %d/%d routes %d/%d footpaths", len(ds.Stops()), len(src.Stops()), len(ds.Routes()), len(src.Routes()), len(ds.Footpaths()), len(src.Footpaths()))
	}
	trips := 0
	for i, r := range ds.Routes() {
		trips += len(r.Trips)
		if len(r.Trips) != len(src.Routes()[i].Trips) {
			t.Fatalf("route %d trips %d vs %d", i, len(r.Trips), len(src.Routes()[i].Trips))
		}
	}
	if trips == 0 {
		t.Fatal("no trips")
	}
}

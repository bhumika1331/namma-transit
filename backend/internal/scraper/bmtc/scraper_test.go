package bmtc

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/store/sqlite"
)

type memRaw struct{ n int }

func (m *memRaw) SaveRaw(context.Context, string, string, string, int, []byte, time.Time) error {
	m.n++
	return nil
}

func TestClientRetriesAndArchives(t *testing.T) {
	f, srv := newFixture(t)
	raw := &memRaw{}
	c := NewClient(srv.URL, 1000, raw)
	c.RetryBase = time.Millisecond
	f.fail500.Store(2)
	var st []ServiceType
	if err := c.Call(context.Background(), "GetAllServiceTypes", map[string]any{}, &st); err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || c.Calls != 3 || raw.n != 3 {
		t.Fatalf("types %d calls %d raw %d", len(st), c.Calls, raw.n)
	}
	// Issuccess=false surfaces as an error, not a shape error.
	var tt []TimetableItem
	err := c.Call(context.Background(), "GetTimetableByRouteid_v3", map[string]any{"routeid": 201}, &tt)
	if err == nil || errors.Is(err, ErrShape) {
		t.Fatalf("want api error, got %v", err)
	}
	// Non-JSON body is a shape error.
	if err := DecodeEnvelope("x", []byte("<html>"), &tt); !errors.Is(err, ErrShape) {
		t.Fatalf("want ErrShape, got %v", err)
	}
	// 404 is not retried.
	c.Calls = 0
	if _, err := c.Post(context.Background(), "Nope", map[string]any{}); err == nil || c.Calls != 1 {
		t.Fatalf("404: err %v calls %d", err, c.Calls)
	}
}

func TestParamsHashIsOrderIndependent(t *testing.T) {
	a := paramsHash([]byte(`{"a":1,"b":"x"}`))
	b := paramsHash([]byte(`{"b":"x","a":1}`))
	if a != b || len(a) != 16 {
		t.Fatalf("%s vs %s", a, b)
	}
}

func TestScrapeCoreAndFaresIntoStore(t *testing.T) {
	_, srv := newFixture(t)
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "s.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	sc := &Scraper{Client: NewClient(srv.URL, 1000, st), Log: log}

	runID, _ := st.StartRun(ctx, "core")
	ds, prog, err := sc.ScrapeCore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Partial {
		t.Fatalf("unexpected partial: %v", prog.Notes)
	}
	// BROKEN has no parent -> one error; the other two routes load.
	if prog.Errors != 1 || len(ds.Routes()) != 3 || len(ds.Stops()) != 5 {
		t.Fatalf("errors %d routes %d stops %d", prog.Errors, len(ds.Routes()), len(ds.Stops()))
	}
	var up, v2 *domain.Route
	for i := range ds.routes {
		r := &ds.routes[i]
		switch r.SourceID {
		case "101/UP":
			up = r
		case "201/UP":
			v2 = r
		}
	}
	if up == nil || v2 == nil {
		t.Fatalf("routes %+v", ds.routes)
	}
	if up.Class != domain.ClassOrdinary || v2.Class != domain.ClassVajra || up.Headsign != "D" {
		t.Fatalf("classes %v %v headsign %q", up.Class, v2.Class, up.Headsign)
	}
	if up.DistKm[3] != 4.1 {
		t.Fatalf("api distances not used: %v", up.DistKm)
	}
	// A->E is ~2.2 km east, E->C ~3.1 km back north-west: ~5.3 km chained.
	if v2.DistKm[2] < 5.0 || v2.DistKm[2] > 5.6 {
		t.Fatalf("haversine chain: %v", v2.DistKm)
	}
	// Timetable: 3 trips sorted, interpolated by distance, past-midnight end handled.
	if len(up.Trips) != 3 || up.Trips[0].Dep[0] != 7*3600+30*60 || up.Trips[2].Arr[3] != 24*3600+10*60 {
		t.Fatalf("trips %+v", up.Trips)
	}
	// Stop B at 1.2/4.1 of a 20 min trip from 08:00 -> ~08:05:51.
	if d := up.Trips[1].Arr[1] - up.Trips[1].Dep[0]; d < 340 || d > 360 {
		t.Fatalf("interpolated arrival offset %d", d)
	}
	if len(v2.Trips) != 0 {
		t.Fatalf("V-2 should have no trips (API said no data)")
	}
	if err := st.ReplaceDataset(ctx, ds); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishRun(ctx, runID, "ok", prog.Calls, prog.Errors, prog.Notes); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.RawCount(ctx); n < 8 {
		t.Fatalf("raw archive has %d rows", n)
	}
	status, _, _ := st.LastRun(ctx, "core")
	if status != "ok" {
		t.Fatalf("run status %q", status)
	}

	// Fares: only route 1 UP and V-2 UP start at stop A (id 1).
	loaded, err := st.LoadDataset(ctx, "bmtc")
	if err != nil {
		t.Fatal(err)
	}
	fs := &testFareStore{st}
	fprog, err := sc.ScrapeFares(ctx, loaded.Routes(), loaded.Stops(), ds.ServiceTs, fs, Shard{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if fprog.Routes != 3 {
		t.Fatalf("fare routes %d", fprog.Routes)
	}
	again, err := st.LoadDataset(ctx, "bmtc")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again.Routes() {
		switch r.SourceID {
		case "101/UP":
			// fares 6,12,18 at positions 1,2,3 -> new stages start at 2 and 3.
			if len(r.StageStarts) != 2 || r.StageStarts[0] != 2 || r.StageStarts[1] != 3 {
				t.Fatalf("1 UP stage starts %v", r.StageStarts)
			}
			if r.StagesBetween(0, 1) != 1 || r.StagesBetween(0, 3) != 3 || r.StagesBetween(1, 2) != 2 {
				t.Fatalf("stages between wrong")
			}
		case "201/UP":
			// fares 15 (E), 20 (C) -> one boundary at position 2.
			if len(r.StageStarts) != 1 || r.StageStarts[0] != 2 {
				t.Fatalf("V-2 stage starts %v", r.StageStarts)
			}
		case "102/DOWN":
			if len(r.StageStarts) != 0 {
				t.Fatalf("DOWN should have no boundaries (origin D not in fixture): %v", r.StageStarts)
			}
		}
	}
	// Boundaries survive a core re-scrape.
	if err := st.ReplaceDataset(ctx, ds); err != nil {
		t.Fatal(err)
	}
	third, _ := st.LoadDataset(ctx, "bmtc")
	kept := 0
	for _, r := range third.Routes() {
		kept += len(r.StageStarts)
	}
	if kept != 3 {
		t.Fatalf("boundaries lost on re-scrape: %d", kept)
	}
	if cur, _ := st.FareCursor(ctx); cur != "" {
		t.Fatalf("cursor should reset after a full pass, got %q", cur)
	}
}

func TestShardAndStageStarts(t *testing.T) {
	sh := Shard{Index: 1, Count: 3}
	if sh.owns(0) || !sh.owns(1) || sh.owns(2) || !sh.owns(4) {
		t.Fatal("shard ownership")
	}
	got := stageStarts([]int32{0, 600, 600, 1200, 1200, 1800}, []bool{false, true, true, false, true, true})
	if len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("starts %v", got)
	}
}

type testFareStore struct{ *sqlite.Store }

func (f *testFareStore) SaveFareSamples(ctx context.Context, routeSourceID string, samples []FareSampleRec) error {
	out := make([]sqlite.FareSample, len(samples))
	for i, s := range samples {
		out[i] = sqlite.FareSample{FromCode: s.FromCode, ToCode: s.ToCode, ServiceTypeID: s.ServiceTypeID, FarePaise: s.FarePaise}
	}
	return f.Store.SaveFareSamples(ctx, routeSourceID, out)
}

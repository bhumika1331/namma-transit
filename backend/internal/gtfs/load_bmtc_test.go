package gtfs

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// TestLoadBMTC is slow (1.5M stop_times) and needs the 42 MB feed; run with
// NAMMA_BMTC_TEST=1 go test ./internal/gtfs -run BMTC -v
func TestLoadBMTC(t *testing.T) {
	if os.Getenv("NAMMA_BMTC_TEST") == "" {
		t.Skip("set NAMMA_BMTC_TEST=1")
	}
	start := time.Now()
	ds, err := Load("../../data/gtfs/bmtc.zip", Options{Name: "bmtc"})
	if err != nil {
		t.Fatal(err)
	}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	trips, byClass := 0, map[domain.ServiceClass]int{}
	for _, r := range ds.Routes() {
		trips += len(r.Trips)
		byClass[r.Class]++
	}
	t.Logf("loaded in %s: %d stops, %d route patterns, %d trips, heap %d MB, classes %v",
		time.Since(start).Round(time.Millisecond), len(ds.Stops()), len(ds.Routes()), trips, ms.HeapAlloc>>20, byClass)
	if len(ds.Stops()) < 8000 || len(ds.Routes()) < 4000 {
		t.Fatalf("suspiciously small feed")
	}
}

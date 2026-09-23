package gtfs

import (
	"os"
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

const bmrclZip = "../../data/gtfs/bmrcl.zip"

func TestClassFromRouteNumber(t *testing.T) {
	cases := map[string]domain.ServiceClass{
		"500D":       domain.ClassOrdinary,
		"V-500D":     domain.ClassVajra,
		"KIA-9":      domain.ClassVayuVajra,
		"MF-12":      domain.ClassMetroFeeder,
		"G-4":        domain.ClassOrdinary,
		"244-C VSD":  domain.ClassOrdinary,
		"VAYU VAJRA": domain.ClassVayuVajra,
	}
	for in, want := range cases {
		if got := ClassFromRouteNumber(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestParseTime(t *testing.T) {
	if s, err := parseTime("25:03:10"); err != nil || s != 25*3600+3*60+10 {
		t.Fatalf("got %d %v", s, err)
	}
	if _, err := parseTime(""); err == nil {
		t.Fatal("empty time should fail")
	}
}

func TestLoadBMRCL(t *testing.T) {
	if _, err := os.Stat(bmrclZip); err != nil {
		t.Skipf("feed not downloaded: %v", err)
	}
	ds, err := Load(bmrclZip, Options{Name: "bmrcl"})
	if err != nil {
		t.Fatal(err)
	}
	// 83 stations; Majestic and RV Road appear once per line -> 85 stops.
	if got := len(ds.Stops()); got != 85 {
		t.Errorf("stops = %d, want 85", got)
	}
	// Four interchange footpaths (2 stations x 2 directions).
	if got := len(ds.Footpaths()); got != 4 {
		t.Errorf("footpaths = %d, want 4", got)
	}
	// Full-length patterns in both directions for 3 lines plus short-turns.
	if got := len(ds.Routes()); got < 6 || got > 25 {
		t.Errorf("routes = %d, want between 6 and 25", got)
	}
	var full *domain.Route
	for i := range ds.routes {
		r := &ds.routes[i]
		if r.Mode != domain.ModeMetroRail || r.Class != domain.ClassMetro {
			t.Fatalf("route %s has mode %v class %v", r.ShortName, r.Mode, r.Class)
		}
		if len(r.Stops) != len(r.DistKm) {
			t.Fatalf("route %s: %d stops, %d dists", r.ShortName, len(r.Stops), len(r.DistKm))
		}
		for _, tr := range r.Trips {
			if len(tr.Arr) != len(r.Stops) || len(tr.Dep) != len(r.Stops) {
				t.Fatalf("route %s: trip length mismatch", r.ShortName)
			}
		}
		for i := 1; i < len(r.Trips); i++ {
			if r.Trips[i].Dep[0] < r.Trips[i-1].Dep[0] {
				t.Fatalf("route %s: trips not sorted", r.ShortName)
			}
		}
		if r.ShortName == "Purple Line" && len(r.Stops) == 37 && full == nil {
			full = r
		}
	}
	if full == nil {
		t.Fatal("no 37-stop Purple Line pattern")
	}
	if full.LineColor == "" {
		t.Error("Purple Line has no colour")
	}
	// Purple line is ~45.7 km end to end.
	if km := full.DistKm[len(full.DistKm)-1]; km < 40 || km > 50 {
		t.Errorf("Purple Line length = %.1f km, want ~45.7", km)
	}
	// Weekday (Tue-Sat service) and Saturday trips exist; Sunday too.
	var wd, sat, sun int
	for _, tr := range full.Trips {
		if tr.Days.Has(domain.Weekday) {
			wd++
		}
		if tr.Days.Has(domain.Saturday) {
			sat++
		}
		if tr.Days.Has(domain.Sunday) {
			sun++
		}
	}
	if wd < 100 || sat < 100 || sun < 50 {
		t.Errorf("trips by day: weekday %d saturday %d sunday %d", wd, sat, sun)
	}
	// Metro stop naming and sublabel.
	s := ds.Stops()[full.Stops[0]]
	if s.Kind != domain.StopMetro || s.SubLabel != "Purple Line" {
		t.Errorf("first stop %+v", s)
	}
}

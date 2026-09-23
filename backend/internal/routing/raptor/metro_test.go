package raptor

import (
	"os"
	"strings"
	"testing"

	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/gtfs"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
	"github.com/bhumika1331/namma-transit/backend/internal/timeprovider"
)

const bmrclZip = "../../../data/gtfs/bmrcl.zip"

func metroRouter(t *testing.T) *Router {
	if _, err := os.Stat(bmrclZip); err != nil {
		t.Skipf("feed not downloaded: %v", err)
	}
	ds, err := gtfs.Load(bmrclZip, gtfs.Options{Name: "bmrcl"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := network.Build([]network.Source{ds}, network.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &Router{Net: n, Time: timeprovider.NewSchedule()}
}

func findStops(n *network.Network, name string) []Access {
	var out []Access
	for _, s := range n.Stops {
		if strings.Contains(strings.ToLower(s.Name), strings.ToLower(name)) {
			out = append(out, Access{Stop: s.ID})
		}
	}
	return out
}

func TestMetroMajesticToIndiranagar(t *testing.T) {
	r := metroRouter(t)
	src := findStops(r.Net, "Majestic")
	dst := findStops(r.Net, "Indiranagar")
	if len(src) != 2 || len(dst) != 1 {
		t.Fatalf("stops: majestic %d indiranagar %d", len(src), len(dst))
	}
	js := r.Plan(Query{Sources: src, Targets: dst, Depart: 10 * h, Day: domain.Weekday})
	if len(js) != 1 || js[0].Rides != 1 {
		t.Fatalf("want one direct journey, got %+v", js)
	}
	j := js[0]
	// Majestic -> Indiranagar is 7 station hops, roughly 12-18 minutes including wait.
	dur := j.Arrive - j.Depart
	if dur < 10*m || dur > 20*m {
		t.Fatalf("duration %d min", dur/m)
	}
	ride := j.Legs[1]
	if r.Net.Routes[ride.Route].ShortName != "Purple Line" || ride.ToPos-ride.FromPos != 7 {
		t.Fatalf("ride = %+v on %s", ride, r.Net.Routes[ride.Route].ShortName)
	}
}

func TestMetroGreenToPurpleInterchange(t *testing.T) {
	r := metroRouter(t)
	src := findStops(r.Net, "Yelachenahalli")
	dst := findStops(r.Net, "Indiranagar")
	js := r.Plan(Query{Sources: src, Targets: dst, Depart: 10 * h, Day: domain.Weekday})
	if len(js) == 0 {
		t.Fatal("no journey")
	}
	j := js[len(js)-1]
	if j.Rides != 2 {
		t.Fatalf("want 2 rides via Majestic, got %+v", j)
	}
	var lines []string
	var sawInterchangeWalk bool
	for _, l := range j.Legs {
		if l.Kind == LegRide {
			lines = append(lines, r.Net.Routes[l.Route].ShortName)
		} else if l.From != l.To {
			sawInterchangeWalk = true
			if !strings.Contains(r.Net.Stops[l.From].Name, "Majestic") {
				t.Fatalf("interchange at %s, want Majestic", r.Net.Stops[l.From].Name)
			}
		}
	}
	if strings.Join(lines, ">") != "Green Line>Purple Line" || !sawInterchangeWalk {
		t.Fatalf("lines %v walk %v", lines, sawInterchangeWalk)
	}
	// ~19 stations plus a 6 min interchange: 35-55 min.
	if dur := j.Arrive - j.Depart; dur < 30*m || dur > 60*m {
		t.Fatalf("duration %d min", dur/m)
	}
}

func TestMetroSundayEarlyMorningNoService(t *testing.T) {
	r := metroRouter(t)
	js := r.Plan(Query{Sources: findStops(r.Net, "Majestic"), Targets: findStops(r.Net, "Indiranagar"), Depart: 5*h + 30*m, Day: domain.Sunday})
	// Sunday service starts 07:00; the router must wait, not fail.
	if len(js) != 1 || js[0].Legs[1].Dep < 6*h+55*m {
		t.Fatalf("sunday early = %+v", js)
	}
}

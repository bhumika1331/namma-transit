package places

import (
	"context"
	"errors"
	"testing"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
)

type src struct{ stops []domain.Stop }

func (s *src) Name() string                 { return "t" }
func (s *src) Source() string               { return "test" }
func (s *src) Version() string              { return "1" }
func (s *src) Stops() []domain.Stop         { return s.stops }
func (s *src) Routes() []domain.Route       { return nil }
func (s *src) Footpaths() []domain.Footpath { return nil }

type fakeGeo struct {
	called int
	err    error
	out    []*transitv1.Place
}

func (f *fakeGeo) Suggest(context.Context, string, *domain.LatLng, int) ([]*transitv1.Place, error) {
	f.called++
	return f.out, f.err
}

func index(t *testing.T, geo Geocoder) *Index {
	n, err := network.Build([]network.Source{&src{stops: []domain.Stop{
		{ID: 0, SourceID: "bus:1", Name: "Koramangala Bus Station", Kind: domain.StopBus, Loc: domain.LatLng{Lat: 12.93, Lng: 77.62}},
		{ID: 1, SourceID: "bus:2", Name: "Koramangala 1st Block", Kind: domain.StopBus, Loc: domain.LatLng{Lat: 12.94, Lng: 77.63}},
		{ID: 2, SourceID: "metro:P:IDN", Name: "Indiranagar", Kind: domain.StopMetro, SubLabel: "Purple Line", Loc: domain.LatLng{Lat: 12.98, Lng: 77.64}},
		{ID: 3, SourceID: "bus:3", Name: "Indiranagara 6th Main", Kind: domain.StopBus, Loc: domain.LatLng{Lat: 12.98, Lng: 77.65}},
		{ID: 4, SourceID: "bus:4", Name: "Indiranagara 6th Main", Kind: domain.StopBus, Loc: domain.LatLng{Lat: 12.981, Lng: 77.651}}, // duplicate name collapses
		{ID: 5, SourceID: "bus:5", Name: "Shivajinagar", Kind: domain.StopBus, Loc: domain.LatLng{Lat: 12.99, Lng: 77.60}},
	}}}, network.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return New(n, geo)
}

func names(ps []*transitv1.Place) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func TestSuggestPrefixContainsAndMetroFirst(t *testing.T) {
	idx := index(t, nil)
	got, fromGeo, err := idx.Suggest(context.Background(), "indira", nil, 5)
	if err != nil || fromGeo {
		t.Fatal(err, fromGeo)
	}
	if n := names(got); len(n) != 2 || n[0] != "Indiranagar" || n[1] != "Indiranagara 6th Main" {
		t.Fatalf("got %v", n)
	}
	got, _, _ = idx.Suggest(context.Background(), "bus station", nil, 5)
	if n := names(got); len(n) != 1 || n[0] != "Koramangala Bus Station" {
		t.Fatalf("contains: %v", n)
	}
	if got, _, _ := idx.Suggest(context.Background(), "k", nil, 5); len(got) != 0 {
		t.Fatal("single char should return nothing")
	}
}

func TestSuggestFuzzy(t *testing.T) {
	idx := index(t, nil)
	got, _, _ := idx.Suggest(context.Background(), "koramangla", nil, 5) // missing 'a'
	if n := names(got); len(n) < 1 || n[0] != "Koramangala Bus Station" && n[0] != "Koramangala 1st Block" {
		t.Fatalf("fuzzy: %v", n)
	}
	if got, _, _ := idx.Suggest(context.Background(), "xyzzy plugh", nil, 5); len(got) != 0 {
		t.Fatalf("nonsense matched: %v", names(got))
	}
}

func TestGeocoderFallback(t *testing.T) {
	geo := &fakeGeo{out: []*transitv1.Place{{Id: GeoPlaceID(12.93, 77.61), Name: "Forum Mall", Kind: transitv1.PlaceKind_PLACE_KIND_GEOCODED}}}
	idx := index(t, geo)
	// Strong stop match: geocoder not consulted.
	if _, fromGeo, _ := idx.Suggest(context.Background(), "shivaji", nil, 5); fromGeo || geo.called != 0 {
		t.Fatal("geocoder should not be called for a strong stop match")
	}
	got, fromGeo, err := idx.Suggest(context.Background(), "forum mall", nil, 5)
	if err != nil || !fromGeo || len(got) != 1 || got[0].Name != "Forum Mall" || geo.called != 1 {
		t.Fatalf("fallback: %v %v %v calls=%d", names(got), fromGeo, err, geo.called)
	}
	// Geocoder error with no stop hits surfaces the error.
	geo.err, geo.out = errors.New("down"), nil
	if _, _, err := idx.Suggest(context.Background(), "forum mall", nil, 5); err == nil {
		t.Fatal("want error")
	}
}

func TestGeoPlaceID(t *testing.T) {
	id := GeoPlaceID(12.934843, 77.618977)
	p, ok := ParseGeoPlaceID(id)
	if !ok || p.Lat != 12.934843 || p.Lng != 77.618977 {
		t.Fatalf("%s -> %+v %v", id, p, ok)
	}
	if _, ok := ParseGeoPlaceID("bus:12"); ok {
		t.Fatal("bus id parsed as geo")
	}
}

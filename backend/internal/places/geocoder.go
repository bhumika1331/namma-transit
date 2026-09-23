package places

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
	"github.com/bhumika1331/namma-transit/backend/internal/domain"
)

// Bengaluru bounding box used to keep geocoder results in the city.
var bengaluru = struct{ minLng, minLat, maxLng, maxLat float64 }{77.30, 12.70, 77.90, 13.25}

// GeoPlaceID encodes a geocoded point so PlanTrip can resolve it without
// a lookup table: "geo:<lat>,<lng>".
func GeoPlaceID(lat, lng float64) string {
	return fmt.Sprintf("geo:%.6f,%.6f", lat, lng)
}

// ParseGeoPlaceID inverts GeoPlaceID.
func ParseGeoPlaceID(id string) (domain.LatLng, bool) {
	if !strings.HasPrefix(id, "geo:") {
		return domain.LatLng{}, false
	}
	var p domain.LatLng
	if _, err := fmt.Sscanf(strings.TrimPrefix(id, "geo:"), "%f,%f", &p.Lat, &p.Lng); err != nil {
		return domain.LatLng{}, false
	}
	return p, true
}

// Photon is komoot's public OpenStreetMap geocoder. Keyless, ~1 req/s fair
// use, so it is the fallback when no Ola Maps key is configured.
type Photon struct {
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

// NewPhoton returns a client against photon.komoot.io.
func NewPhoton(userAgent string) *Photon {
	return &Photon{
		BaseURL:   "https://photon.komoot.io/api/",
		UserAgent: userAgent,
		Client:    &http.Client{Timeout: 6 * time.Second},
	}
}

func (g *Photon) Suggest(ctx context.Context, query string, bias *domain.LatLng, limit int) ([]*transitv1.Place, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", fmt.Sprint(limit))
	q.Set("lang", "en")
	q.Set("bbox", fmt.Sprintf("%g,%g,%g,%g", bengaluru.minLng, bengaluru.minLat, bengaluru.maxLng, bengaluru.maxLat))
	if bias != nil {
		q.Set("lat", fmt.Sprint(bias.Lat))
		q.Set("lon", fmt.Sprint(bias.Lng))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", g.UserAgent)
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("photon: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Features []struct {
			Properties struct {
				Name     string `json:"name"`
				Street   string `json:"street"`
				Locality string `json:"locality"`
				District string `json:"district"`
				City     string `json:"city"`
				Type     string `json:"type"`
			} `json:"properties"`
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("photon: %w", err)
	}
	var out []*transitv1.Place
	for _, f := range body.Features {
		if len(f.Geometry.Coordinates) != 2 || f.Properties.Name == "" {
			continue
		}
		lng, lat := f.Geometry.Coordinates[0], f.Geometry.Coordinates[1]
		if lat < bengaluru.minLat || lat > bengaluru.maxLat || lng < bengaluru.minLng || lng > bengaluru.maxLng {
			continue
		}
		sub := firstNonEmpty(f.Properties.Locality, f.Properties.District, f.Properties.Street, f.Properties.City)
		out = append(out, &transitv1.Place{
			Id:       GeoPlaceID(lat, lng),
			Name:     f.Properties.Name,
			Kind:     transitv1.PlaceKind_PLACE_KIND_GEOCODED,
			Loc:      &transitv1.LatLng{Lat: lat, Lng: lng},
			SubLabel: sub,
		})
	}
	return out, nil
}

// OlaMaps uses Ola's Places autocomplete API (Indian coverage, free tier
// with an API key). Docs: https://maps.olakrutrim.com/docs
type OlaMaps struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// NewOlaMaps returns a client for the given key.
func NewOlaMaps(apiKey string) *OlaMaps {
	return &OlaMaps{
		BaseURL: "https://api.olamaps.io/places/v1/autocomplete",
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 6 * time.Second},
	}
}

func (g *OlaMaps) Suggest(ctx context.Context, query string, bias *domain.LatLng, limit int) ([]*transitv1.Place, error) {
	q := url.Values{}
	q.Set("input", query)
	q.Set("api_key", g.APIKey)
	loc := domain.LatLng{Lat: 12.9716, Lng: 77.5946}
	if bias != nil {
		loc = *bias
	}
	q.Set("location", fmt.Sprintf("%g,%g", loc.Lat, loc.Lng))
	q.Set("radius", "40000")
	q.Set("strictbounds", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ola maps: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Predictions []struct {
			Description          string `json:"description"`
			StructuredFormatting struct {
				MainText      string `json:"main_text"`
				SecondaryText string `json:"secondary_text"`
			} `json:"structured_formatting"`
			Geometry struct {
				Location struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"location"`
			} `json:"geometry"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("ola maps: %w", err)
	}
	var out []*transitv1.Place
	for _, p := range body.Predictions {
		lat, lng := p.Geometry.Location.Lat, p.Geometry.Location.Lng
		if lat == 0 && lng == 0 {
			continue
		}
		name := firstNonEmpty(p.StructuredFormatting.MainText, p.Description)
		out = append(out, &transitv1.Place{
			Id:       GeoPlaceID(lat, lng),
			Name:     name,
			Kind:     transitv1.PlaceKind_PLACE_KIND_GEOCODED,
			Loc:      &transitv1.LatLng{Lat: lat, Lng: lng},
			SubLabel: p.StructuredFormatting.SecondaryText,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Chain tries geocoders in order until one returns results.
type Chain []Geocoder

func (c Chain) Suggest(ctx context.Context, query string, bias *domain.LatLng, limit int) ([]*transitv1.Place, error) {
	var lastErr error
	for _, g := range c {
		out, err := g.Suggest(ctx, query, bias, limit)
		if err != nil {
			lastErr = err
			continue
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, lastErr
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Package bmrcl cross-checks the metro network against a server-rendered
// public station list using goquery. BMRCL's own site is client-rendered,
// so the Wikipedia station table is the stable source; the result is used
// to flag stations missing from the loaded feed (e.g. when a new line
// opens) rather than to build the network.
package bmrcl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// DefaultURL is the station list page.
const DefaultURL = "https://en.wikipedia.org/wiki/List_of_Namma_Metro_stations"

// Station is one operational station row.
type Station struct {
	Name         string   `json:"name"`
	Lines        []string `json:"lines"`
	Opened       string   `json:"opened"`
	Layout       string   `json:"layout"`
	Abbreviation string   `json:"abbreviation"`
	Interchange  bool     `json:"interchange"`
}

// Fetch downloads and parses the page.
func Fetch(ctx context.Context, url, userAgent string) ([]Station, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bmrcl stations: HTTP %d", resp.StatusCode)
	}
	return Parse(resp.Body)
}

var lineRe = regexp.MustCompile(`(?i)(purple|green|yellow|pink|blue|red|orange|grey|silver|magenta)\s*line`)

// Parse extracts the first "Station name / Line / Opened" table, which is
// the operational network; later tables are under construction or planned.
func Parse(r io.Reader) ([]Station, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	var out []Station
	doc.Find("table.wikitable").EachWithBreak(func(_ int, table *goquery.Selection) bool {
		headers := table.Find("tr").First().Find("th").Map(func(_ int, th *goquery.Selection) string {
			return strings.ToLower(strings.TrimSpace(th.Text()))
		})
		if len(headers) < 3 || !strings.HasPrefix(headers[0], "station name") || headers[1] != "line" {
			return true // keep looking
		}
		table.Find("tr").Each(func(i int, tr *goquery.Selection) {
			cells := tr.Find("td")
			if cells.Length() < 5 {
				return
			}
			text := func(i int) string { return clean(cells.Eq(i).Text()) }
			name := text(0)
			// Columns: English, Kannada, Line, Opened, Layout, Abbreviations, ...
			if name == "" {
				return
			}
			st := Station{
				Name:         strings.TrimSpace(strings.TrimSuffix(name, "†")),
				Interchange:  strings.Contains(name, "†"),
				Opened:       text(3),
				Layout:       text(4),
				Abbreviation: text(5),
			}
			for _, m := range lineRe.FindAllString(cells.Eq(2).Text(), -1) {
				st.Lines = append(st.Lines, titleCase(m))
			}
			if len(st.Lines) == 0 {
				return
			}
			out = append(out, st)
		})
		return false // done after the first matching table
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("bmrcl stations: no operational station table found (page layout changed?)")
	}
	return out, nil
}

var noteRe = regexp.MustCompile(`\[[^\]]*\]`)

func clean(s string) string {
	s = noteRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func titleCase(s string) string {
	f := strings.Fields(strings.ToLower(s))
	for i, w := range f {
		f[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(f, " ")
}

// Missing returns station names from the page that no loaded metro stop
// name resembles. Names are compared loosely (case, punctuation, common
// suffixes, then trigram similarity) because the feed and Wikipedia spell
// them differently: "Mysuru Road" vs "Mysore Road", "Yeshwanthpur" vs
// "Yeshwanthpura".
func Missing(stations []Station, feedNames []string) []string {
	have := make([]string, 0, len(feedNames))
	for _, n := range feedNames {
		have = append(have, norm(n))
	}
	var missing []string
	for _, st := range stations {
		want := norm(st.Name)
		found := false
		for _, n := range have {
			if n == want || strings.Contains(n, want) || strings.Contains(want, n) || similarity(want, n) >= 0.5 {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, st.Name)
		}
	}
	return missing
}

// similarity is the Jaccard index of character trigrams.
func similarity(a, b string) float64 {
	ta, tb := trigrams(a), trigrams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	n := 0
	for t := range ta {
		if tb[t] {
			n++
		}
	}
	return float64(n) / float64(len(ta)+len(tb)-n)
}

func trigrams(s string) map[string]bool {
	s = "  " + s + " "
	out := map[string]bool{}
	for i := 0; i+3 <= len(s); i++ {
		out[s[i:i+3]] = true
	}
	return out
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// aliases map official Kannada-derived spellings to the anglicised ones
// the feed uses.
var aliases = [][2]string{{"mysuru", "mysore"}, {"bengaluru", "bangalore"}, {"visveshwaraya", "visvesvaraya"}}

func norm(s string) string {
	s = strings.ToLower(s)
	for _, drop := range []string{" metro station", " station", "nadaprabhu kempegowda station, ", "dr. ", "sir m. "} {
		s = strings.ReplaceAll(s, drop, "")
	}
	for _, a := range aliases {
		s = strings.ReplaceAll(s, a[0], a[1])
	}
	return nonAlnum.ReplaceAllString(s, "")
}

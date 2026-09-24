package bmrcl

import (
	"strings"
	"testing"
)

const fixture = `<html><body>
<table class="wikitable"><tr><th>†</th><td>Interchange station</td></tr></table>
<table class="wikitable">
<tr><th rowspan="2">Station name</th><th rowspan="2">Line</th><th rowspan="2">Opened</th><th>Layout</th><th>Abbreviations</th><th>Notes</th><th>Ref.</th></tr>
<tr><th>English</th><th>Kannada</th></tr>
<tr><td>Attiguppe</td><td>ಅತ್ತಿಗುಪ್ಪೆ</td><td><span>Purple Line</span></td><td>16 November 2015</td><td>Elevated</td><td>AGPP</td><td>–</td><td>[5]</td></tr>
<tr><td>Nadaprabhu Kempegowda Station, Majestic †</td><td>ಮೆಜೆಸ್ಟಿಕ್</td><td>Purple Line<br>Green Line</td><td>29 April 2016</td><td>Underground</td><td>KGWA</td><td>Interchange</td><td>[6]</td></tr>
<tr><td>Rashtreeya Vidyalaya Road †</td><td>ಆರ್.ವಿ. ರಸ್ತೆ</td><td>Green LineYellow Line</td><td>2017</td><td>Elevated</td><td>RVR</td><td></td><td></td></tr>
<tr><td>Mysuru Road</td><td>ಮೈಸೂರು</td><td>Purple Line</td><td>2015</td><td>Elevated</td><td>MYRD</td><td></td><td></td></tr>
<tr><td>Brand New Station</td><td>ಹೊಸ</td><td>Yellow Line</td><td>2026</td><td>Elevated</td><td>BNS</td><td></td><td></td></tr>
</table>
<table class="wikitable">
<tr><th>Station name</th><th>Line</th><th>Opened</th><th>Layout</th><th>Abbreviations</th></tr>
<tr><td>Agara †</td><td>ಅಗರ</td><td>Blue Line</td><td>December 2026</td><td>Elevated</td><td>TBC</td></tr>
</table>
</body></html>`

func TestParse(t *testing.T) {
	st, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 5 {
		t.Fatalf("stations = %d: %+v", len(st), st)
	}
	if st[0].Name != "Attiguppe" || st[0].Lines[0] != "Purple Line" || st[0].Abbreviation != "AGPP" || st[0].Interchange {
		t.Fatalf("first = %+v", st[0])
	}
	if !st[1].Interchange || len(st[1].Lines) != 2 || st[1].Name != "Nadaprabhu Kempegowda Station, Majestic" {
		t.Fatalf("majestic = %+v", st[1])
	}
	if strings.Join(st[2].Lines, ",") != "Green Line,Yellow Line" {
		t.Fatalf("rvr lines = %v", st[2].Lines)
	}
}

func TestMissing(t *testing.T) {
	st, _ := Parse(strings.NewReader(fixture))
	feed := []string{"Attigupe", "Nadaprabhu Kempegowda Station, Majestic", "Rashtriya Vidyalaya Road", "Mysore Road"}
	got := Missing(st, feed)
	if len(got) != 1 || got[0] != "Brand New Station" {
		t.Fatalf("missing = %v", got)
	}
}

func TestParseNoTable(t *testing.T) {
	if _, err := Parse(strings.NewReader("<html><table class='wikitable'><tr><th>x</th></tr></table></html>")); err == nil {
		t.Fatal("want error")
	}
}

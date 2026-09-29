package scenic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/uber/h3-go/v4"
)

const rulesText = `# green and water areas
a/leisure=park,garden
a/water
`

func rules(t *testing.T) Rules {
	t.Helper()
	r, err := ParseRules(strings.NewReader(rulesText))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseRules(t *testing.T) {
	r := rules(t)
	for tags, want := range map[string]bool{
		"leisure=park":       true,
		"leisure=garden":     true,
		"leisure=playground": false,
		"water=pond":         true,
		"landuse=grass":      false,
	} {
		k, v, _ := strings.Cut(tags, "=")
		if got := r.Match(map[string]string{k: v}); got != want {
			t.Errorf("%s: match = %v, want %v", tags, got, want)
		}
	}
	for _, bad := range []string{"n/amenity=cafe", "a/leisure!=park", "a/", "leisure=park", "a/=park"} {
		if _, err := ParseRules(strings.NewReader(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

var permCenter = h3.NewLatLng(58.01, 56.25)

func cellAt(t *testing.T, ll h3.LatLng) h3.Cell {
	t.Helper()
	c, err := h3.LatLngToCell(ll, CellResolution)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// outline is a cell's boundary as GeoJSON coordinates, closed as GeoJSON requires.
func outline(t *testing.T, c h3.Cell) [][2]float64 {
	t.Helper()
	boundary, err := c.Boundary()
	if err != nil {
		t.Fatal(err)
	}
	out := make([][2]float64, 0, len(boundary)+1)
	for _, p := range boundary {
		out = append(out, [2]float64{p.Lng, p.Lat})
	}
	return append(out, out[0])
}

func feature(t *testing.T, tags map[string]string, polygons ...[][][2]float64) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type":       "Feature",
		"geometry":   map[string]any{"type": "MultiPolygon", "coordinates": polygons},
		"properties": tags,
	})
	if err != nil {
		t.Fatal(err)
	}
	return "\x1e" + string(b) + "\n"
}

func cover(t *testing.T, lines ...string) Shares {
	t.Helper()
	shares, err := Cover(strings.NewReader(strings.Join(lines, "")), rules(t))
	if err != nil {
		t.Fatal(err)
	}
	return shares
}

func TestCoverWholeCell(t *testing.T) {
	c := cellAt(t, permCenter)
	shares := cover(t, feature(t, map[string]string{"leisure": "park"}, [][][2]float64{outline(t, c)}))
	if shares[c] < 0.85 || shares[c] > 1 {
		t.Fatalf("share of the covered cell = %f", shares[c])
	}
	for cell, share := range shares {
		if cell != c && share > 0.15 {
			t.Fatalf("neighbour %s share = %f", cell, share)
		}
	}
}

func TestCoverSubtractsHoles(t *testing.T) {
	c := cellAt(t, permCenter)
	children, err := c.Children(CellResolution + 1)
	if err != nil {
		t.Fatal(err)
	}
	whole := cover(t, feature(t, map[string]string{"leisure": "park"}, [][][2]float64{outline(t, c)}))
	holed := cover(
		t,
		feature(t, map[string]string{"leisure": "park"}, [][][2]float64{outline(t, c), outline(t, children[0])}),
	)
	if diff := whole[c] - holed[c]; math.Abs(diff-1.0/7) > 0.05 {
		t.Fatalf("hole removed %f of the cell, want about 1/7", diff)
	}
}

func TestCoverIgnoresOtherAreasAndOverlaps(t *testing.T) {
	c := cellAt(t, permCenter)
	park := feature(t, map[string]string{"leisure": "park"}, [][][2]float64{outline(t, c)})
	once := cover(t, park)
	twice := cover(t, park, park, feature(t, map[string]string{"water": "pond"}, [][][2]float64{outline(t, c)}))
	if once[c] != twice[c] {
		t.Fatalf("overlapping areas counted twice: %f vs %f", once[c], twice[c])
	}
	if housing := cover(
		t,
		feature(t, map[string]string{"landuse": "residential"}, [][][2]float64{outline(t, c)}),
	); len(
		housing,
	) != 0 {
		t.Fatalf("residential area counted: %v", housing)
	}
}

func TestCoverMultiPolygon(t *testing.T) {
	a := cellAt(t, permCenter)
	b := cellAt(t, h3.NewLatLng(58.05, 56.30))
	shares := cover(
		t,
		feature(t, map[string]string{"water": "river"}, [][][2]float64{outline(t, a)}, [][][2]float64{outline(t, b)}),
	)
	if shares[a] < 0.85 || shares[b] < 0.85 {
		t.Fatalf("shares = %f, %f", shares[a], shares[b])
	}
}

func TestCoverRejectsBrokenInput(t *testing.T) {
	if _, err := Cover(strings.NewReader("\x1e{not json\n"), rules(t)); err == nil {
		t.Fatal("broken feature accepted")
	}
}

func TestLayerRoundTrip(t *testing.T) {
	a := cellAt(t, permCenter)
	b := cellAt(t, h3.NewLatLng(58.05, 56.30))
	var buf bytes.Buffer
	if err := WriteLayer(&buf, Shares{b: 0.25, a: 1}); err != nil {
		t.Fatal(err)
	}
	first, second := a, b
	if b < a {
		first, second = b, a
	}
	text := map[h3.Cell]string{a: "1.0000", b: "0.2500"}
	want := fmt.Sprintf("cell,share\n%s,%s\n%s,%s\n", first, text[first], second, text[second])
	if buf.String() != want {
		t.Fatalf("layer =\n%s\nwant\n%s", buf.String(), want)
	}
	read, err := ReadLayer(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 || read[a] != 1 || read[b] != 0.25 {
		t.Fatalf("read = %v", read)
	}
}

func TestReadLayerRejectsBadRows(t *testing.T) {
	c := cellAt(t, permCenter)
	fine, _ := c.Children(CoverResolution)
	for _, bad := range []string{
		"cell,share\nzz,0.5\n",
		fmt.Sprintf("cell,share\n%s,1.5\n", c),
		fmt.Sprintf("cell,share\n%s,0.5\n", fine[0]),
		fmt.Sprintf("header\n%s,0.5\n", c),
	} {
		if _, err := ReadLayer(strings.NewReader(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

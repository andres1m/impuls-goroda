package scenic

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/uber/h3-go/v4"
)

const (
	// CellResolution is the grid the scores are kept in.
	CellResolution = 8
	// CoverResolution measures how much of a cell an area covers, counting the fine cells whose
	// centres lie inside it.
	CoverResolution = 11
	finePerCell     = 343
)

// Shares maps a cell to the part of it covered by green or water areas, in (0, 1].
type Shares map[h3.Cell]float64

// Cover reads GeoJSON text sequence features, one per line and optionally prefixed with the record
// separator, and measures how much of each cell the matching areas cover.
func Cover(features io.Reader, rules Rules) (Shares, error) {
	covered := map[h3.Cell]struct{}{}
	in := bufio.NewReader(features)
	for n := 1; ; n++ {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			if ferr := coverFeature(line, rules, covered); ferr != nil {
				return nil, fmt.Errorf("feature %d: %w", n, ferr)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read feature line: %w", err)
		}
	}
	counts := map[h3.Cell]int{}
	for fine := range covered {
		cell, err := fine.Parent(CellResolution)
		if err != nil {
			return nil, fmt.Errorf("parent cell: %w", err)
		}
		counts[cell]++
	}
	shares := make(Shares, len(counts))
	for cell, n := range counts {
		shares[cell] = min(1, float64(n)/finePerCell)
	}
	return shares, nil
}

type geoFeature struct {
	Geometry struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

// ring is a GeoJSON linear ring of [longitude, latitude] positions.
type ring [][2]float64

func coverFeature(line []byte, rules Rules, covered map[h3.Cell]struct{}) error {
	line = trimRecord(line)
	if len(line) == 0 {
		return nil
	}
	var f geoFeature
	if err := json.Unmarshal(line, &f); err != nil {
		return err
	}
	if !rules.Match(stringTags(f.Properties)) {
		return nil
	}
	polygons, err := decodePolygons(f.Geometry.Type, f.Geometry.Coordinates)
	if err != nil {
		return err
	}
	for _, p := range polygons {
		if err := coverPolygon(p, covered); err != nil {
			return err
		}
	}
	return nil
}

func decodePolygons(geomType string, coords json.RawMessage) ([][]ring, error) {
	switch geomType {
	case "Polygon":
		var p []ring
		if err := json.Unmarshal(coords, &p); err != nil {
			return nil, err
		}
		return [][]ring{p}, nil
	case "MultiPolygon":
		var polygons [][]ring
		if err := json.Unmarshal(coords, &polygons); err != nil {
			return nil, err
		}
		return polygons, nil
	default:
		return nil, nil
	}
}

func coverPolygon(p []ring, covered map[h3.Cell]struct{}) error {
	if len(p) == 0 {
		return nil
	}
	polygon := h3.GeoPolygon{GeoLoop: loop(p[0])}
	for _, hole := range p[1:] {
		polygon.Holes = append(polygon.Holes, loop(hole))
	}
	cells, err := h3.PolygonToCells(polygon, CoverResolution)
	if err != nil {
		return fmt.Errorf("polygon to cells: %w", err)
	}
	for _, c := range cells {
		covered[c] = struct{}{}
	}
	return nil
}

func trimRecord(line []byte) []byte {
	for len(line) > 0 && (line[0] == 0x1e || line[0] == ' ') {
		line = line[1:]
	}
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r' || line[len(line)-1] == ' ') {
		line = line[:len(line)-1]
	}
	return line
}

func stringTags(properties map[string]any) map[string]string {
	tags := make(map[string]string, len(properties))
	for k, v := range properties {
		if s, ok := v.(string); ok {
			tags[k] = s
		}
	}
	return tags
}

// loop drops the closing position GeoJSON repeats; H3 closes loops itself.
func loop(r ring) h3.GeoLoop {
	if len(r) > 1 && r[0] == r[len(r)-1] {
		r = r[:len(r)-1]
	}
	out := make(h3.GeoLoop, len(r))
	for i, p := range r {
		out[i] = h3.NewLatLng(p[1], p[0])
	}
	return out
}

package main

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uber/h3-go/v4"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/scenic"
)

func stage(t *testing.T) (data, rules, scenicDir string) {
	t.Helper()
	data = t.TempDir()
	version := "20260927T000000Z-abcd1234"
	scenicDir = filepath.Join(data, version, "scenic")
	if err := os.MkdirAll(scenicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(data, "next"), version+"\n")
	rules = filepath.Join(t.TempDir(), "scenic.conf")
	write(t, rules, "a/leisure=park\n")
	cell, err := h3.LatLngToCell(h3.NewLatLng(58.01, 56.25), scenic.CellResolution)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := cell.Boundary()
	if err != nil {
		t.Fatal(err)
	}
	var loop [][2]float64
	for _, p := range boundary {
		loop = append(loop, [2]float64{p.Lng, p.Lat})
	}
	feature, err := json.Marshal(map[string]any{
		"type":       "Feature",
		"geometry":   map[string]any{"type": "Polygon", "coordinates": [][][2]float64{append(loop, loop[0])}},
		"properties": map[string]string{"leisure": "park"},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(scenicDir, "perm.geojsonseq"), "\x1e"+string(feature)+"\n")
	return data, rules, scenicDir
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

var quiet = log.New(io.Discard, "", 0)

func TestRunBuildsCityLayers(t *testing.T) {
	data, rules, dir := stage(t)
	if err := run(data, rules, quiet); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "perm.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	shares, err := scenic.ReadLayer(f)
	if err != nil || len(shares) == 0 {
		t.Fatalf("layer %v, %v", shares, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "perm.geojsonseq")); !os.IsNotExist(err) {
		t.Fatal("the source features are kept")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("scenic directory holds %d files", len(entries))
	}
}

func TestRunKeepsBuiltLayer(t *testing.T) {
	data, rules, dir := stage(t)
	write(t, filepath.Join(dir, "perm.csv"), "cell,share\n")
	if err := run(data, rules, quiet); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "perm.csv")); string(b) != "cell,share\n" {
		t.Fatalf("built layer was rebuilt: %q", b)
	}
}

func TestRunWithoutStagedFeatures(t *testing.T) {
	data, rules, dir := stage(t)
	if err := os.Remove(filepath.Join(dir, "perm.geojsonseq")); err != nil {
		t.Fatal(err)
	}
	if err := run(data, rules, quiet); err == nil || !strings.Contains(err.Error(), "no scenic") {
		t.Fatalf("err = %v", err)
	}
}

package scenic

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/uber/h3-go/v4"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func point(ll h3.LatLng) domain.Coordinate {
	return domain.Coordinate{Latitude: ll.Lat, Longitude: ll.Lng}
}

func near(t *testing.T) (a, b h3.Cell, from, to domain.Coordinate) {
	t.Helper()
	a = cellAt(t, permCenter)
	ring, err := a.GridDisk(1)
	if err != nil {
		t.Fatal(err)
	}
	b = ring[len(ring)-1]
	centreA, _ := a.LatLng()
	centreB, _ := b.LatLng()
	return a, b, point(centreA), point(centreB)
}

func TestGridCellScore(t *testing.T) {
	a, b, from, to := near(t)
	for name, tc := range map[string]struct {
		places map[h3.Cell]int
		shares Shares
		want   float64
	}{
		"bare":                               {nil, Shares{}, 0},
		"places saturate":                    {map[h3.Cell]int{a: 5, b: 3}, Shares{}, 0.5},
		"places and green halve":             {map[h3.Cell]int{a: 3, b: 0}, Shares{a: 1, b: 0.5}, (1 + 0.25) / 2},
		"without a layer places count whole": {map[h3.Cell]int{a: 3, b: 0}, nil, 0.5},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewGrid(tc.places, tc.shares)
			if got := g.Score(from, to); math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("score = %f, want %f", got, tc.want)
			}
		})
	}
}

func TestGridAveragesThePath(t *testing.T) {
	a := cellAt(t, permCenter)
	far := cellAt(t, h3.NewLatLng(58.03, 56.25))
	path, err := h3.GridPath(a, far)
	if err != nil || len(path) < 3 {
		t.Fatalf("path %v, %v", path, err)
	}
	middle := path[len(path)/2]
	g := NewGrid(nil, Shares{middle: 1})
	centreA, _ := a.LatLng()
	centreFar, _ := far.LatLng()
	want := 0.5 / float64(len(path))
	if got := g.Score(point(centreA), point(centreFar)); math.Abs(got-want) > 1e-9 {
		t.Fatalf("score = %f, want %f over %d cells", got, want, len(path))
	}
	if got := g.Score(point(centreA), point(centreA)); got != 0 {
		t.Fatalf("staying in a bare cell scores %f", got)
	}
}

func TestGridIsSafeForParallelSearch(t *testing.T) {
	_, _, from, to := near(t)
	g := NewGrid(nil, Shares{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				g.Score(from, to)
			}
		})
	}
	wg.Wait()
}

func TestLoadLayers(t *testing.T) {
	dir := t.TempDir()
	a := cellAt(t, permCenter)
	f, err := os.Create(filepath.Join(dir, "perm.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := WriteLayer(f, Shares{a: 0.5}); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	layers, err := LoadLayers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 || layers["perm"][a] != 0.5 {
		t.Fatalf("layers = %v", layers)
	}
	if missing, loadErr := LoadLayers(filepath.Join(dir, "absent")); loadErr != nil || len(missing) != 0 {
		t.Fatalf("missing directory: %v, %v", missing, loadErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, "moscow.csv"), []byte("broken"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, loadErr := LoadLayers(dir); loadErr == nil {
		t.Fatal("broken layer accepted")
	}
}

type countingReader struct {
	calls  int
	places map[h3.Cell]int
	err    error
}

func (r *countingReader) ScenicPlaces(context.Context, string) (map[h3.Cell]int, error) {
	r.calls++
	return r.places, r.err
}

func TestSourceCachesByRevision(t *testing.T) {
	a, _, from, to := near(t)
	reader := &countingReader{places: map[h3.Cell]int{a: 3}}
	s := NewSource(reader, map[string]Shares{"perm": {a: 1}})
	ctx := context.Background()
	g, err := s.Grid(ctx, "perm", 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Score(from, to); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("score = %f", got)
	}
	if _, gridErr := s.Grid(ctx, "perm", 7); gridErr != nil || reader.calls != 1 {
		t.Fatalf("same revision read %d times, %v", reader.calls, gridErr)
	}
	if _, gridErr := s.Grid(ctx, "perm", 8); gridErr != nil || reader.calls != 2 {
		t.Fatalf("new revision read %d times, %v", reader.calls, gridErr)
	}
	moscow, err := s.Grid(ctx, "moscow", 8)
	if err != nil || reader.calls != 3 {
		t.Fatalf("another city read %d times, %v", reader.calls, err)
	}
	if got := moscow.Score(from, to); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("city without a layer scores %f", got)
	}
	reader.err = errors.New("down")
	if _, gridErr := s.Grid(ctx, "perm", 9); gridErr == nil {
		t.Fatal("reader failure hidden")
	}
	if _, gridErr := s.Grid(ctx, "perm", 8); gridErr != nil {
		t.Fatalf("cached revision lost after a failure: %v", gridErr)
	}
}

func TestGridStopsCachingAtTheLimit(t *testing.T) {
	a, _, from, _ := near(t)
	ring, err := a.GridDisk(1)
	if err != nil {
		t.Fatal(err)
	}
	places := map[h3.Cell]int{ring[1]: 2}
	limited, fresh := NewGrid(places, nil), NewGrid(places, nil)
	limited.walkLimit = 2
	for _, c := range ring[1:4] {
		centre, _ := c.LatLng()
		if got, want := limited.Score(from, point(centre)), fresh.Score(from, point(centre)); got != want {
			t.Fatalf("score %v past the limit, want %v", got, want)
		}
	}
	cached := 0
	limited.walks.Range(func(_, _ any) bool { cached++; return true })
	if cached != 2 {
		t.Fatalf("%d walks cached, limit 2", cached)
	}
}

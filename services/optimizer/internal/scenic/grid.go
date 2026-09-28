package scenic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/uber/h3-go/v4"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// placesForFullScore scenic places in a cell make its catalog part as scenic as it gets.
const placesForFullScore = 3

// maxWalks bounds the walk cache of one city; past it scores are computed on every call.
const maxWalks = 1 << 16

// Grid scores walks in one city from its catalog's scenic places and, when the city has one, its
// layer of green and water areas.
type Grid struct {
	places map[h3.Cell]int
	// Nil when the city has no layer; the catalog then decides alone.
	shares Shares
	// Walk scores by pair of end cells; the search asks for the same walks many times.
	walks     sync.Map
	cached    atomic.Int64
	walkLimit int64
}

func NewGrid(places map[h3.Cell]int, shares Shares) *Grid {
	return &Grid{places: places, shares: shares, walkLimit: maxWalks}
}

func (g *Grid) cell(c h3.Cell) float64 {
	places := min(1, float64(g.places[c])/placesForFullScore)
	if g.shares == nil {
		return places
	}
	return 0.5*places + 0.5*g.shares[c]
}

// Score averages the cells on the straight grid path between the ends. The path only samples the
// surroundings; it says nothing about whether the way can be walked.
func (g *Grid) Score(from, to domain.Coordinate) float64 {
	a, errFrom := h3.LatLngToCell(h3.NewLatLng(from.Latitude, from.Longitude), CellResolution)
	b, errTo := h3.LatLngToCell(h3.NewLatLng(to.Latitude, to.Longitude), CellResolution)
	if errFrom != nil || errTo != nil {
		return 0
	}
	key := [2]h3.Cell{a, b}
	if score, ok := g.walks.Load(key); ok {
		return score.(float64)
	}
	path, err := h3.GridPath(a, b)
	if err != nil || len(path) == 0 {
		// Far apart or across a pentagon the grid has no path; the ends still describe the walk.
		path = []h3.Cell{a, b}
	}
	sum := 0.0
	for _, c := range path {
		sum += g.cell(c)
	}
	score := sum / float64(len(path))
	if g.cached.Load() < g.walkLimit {
		if _, loaded := g.walks.LoadOrStore(key, score); !loaded {
			g.cached.Add(1)
		}
	}
	return score
}

// LoadLayers reads every city's layer from dir, named <city>.csv. A missing directory means no
// layers, which is how a stand without routing data runs.
func LoadLayers(dir string) (map[string]Shares, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil {
		return nil, err
	}
	layers := make(map[string]Shares, len(files))
	for _, path := range files {
		shares, err := readLayerFile(path)
		if err != nil {
			return nil, err
		}
		layers[strings.TrimSuffix(filepath.Base(path), ".csv")] = shares
	}
	return layers, nil
}

func readLayerFile(path string) (Shares, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	shares, err := ReadLayer(f)
	if err != nil {
		return nil, errors.Join(errors.New(filepath.Base(path)), err)
	}
	return shares, nil
}

// DensityReader counts the catalog's scenic places per cell of a city.
type DensityReader interface {
	ScenicPlaces(ctx context.Context, city string) (map[h3.Cell]int, error)
}

// Source keeps each city's grid for the newest catalog revision it has seen.
type Source struct {
	places DensityReader
	layers map[string]Shares
	mu     sync.Mutex
	grids  map[string]revisionGrid
}

type revisionGrid struct {
	revision domain.CatalogRevision
	grid     *Grid
}

func NewSource(places DensityReader, layers map[string]Shares) *Source {
	return &Source{places: places, layers: layers, grids: map[string]revisionGrid{}}
}

func (s *Source) Grid(ctx context.Context, city string, revision domain.CatalogRevision) (*Grid, error) {
	s.mu.Lock()
	cached, ok := s.grids[city]
	s.mu.Unlock()
	if ok && cached.revision == revision {
		return cached.grid, nil
	}
	places, err := s.places.ScenicPlaces(ctx, city)
	if err != nil {
		return nil, err
	}
	grid := NewGrid(places, s.layers[city])
	s.mu.Lock()
	if cached, ok := s.grids[city]; !ok || cached.revision <= revision {
		s.grids[city] = revisionGrid{revision: revision, grid: grid}
	}
	s.mu.Unlock()
	return grid, nil
}

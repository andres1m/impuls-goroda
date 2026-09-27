package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// maxTableBytes bounds a table response well above the largest table the routers are allowed to
// build, so a misbehaving endpoint cannot exhaust memory.
const maxTableBytes = 64 << 20

// Client queries OSRM routers over HTTP.
type Client struct {
	http *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	return &Client{http: httpClient}
}

// Table holds travel between every pair of the requested points on one routing graph.
type Table struct {
	// Version of the graph data; the router reports it with every answer.
	Version string
	// How far each point had to move to reach the street network.
	SnapMeters []float64
	size       int
	seconds    []*float64
	meters     []*float64
}

// Pair returns the travel from point i to point j; false when the graph has no path.
func (t Table) Pair(i, j int) (seconds, meters float64, ok bool) {
	k := i*t.size + j
	if t.seconds[k] == nil || t.meters[k] == nil {
		return 0, 0, false
	}
	return *t.seconds[k], *t.meters[k], true
}

type tableResponse struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	DataVersion string       `json:"data_version"`
	Durations   [][]*float64 `json:"durations"`
	Distances   [][]*float64 `json:"distances"`
	Sources     []struct {
		Distance float64 `json:"distance"`
	} `json:"sources"`
}

// Table asks the router for durations and distances between all points. Snapping is not limited
// here, because OSRM rejects the whole table when a single point is out of range; callers judge
// SnapMeters themselves.
func (c *Client) Table(ctx context.Context, endpoint, profile string, points []domain.Coordinate) (Table, error) {
	if len(points) == 0 {
		return Table{}, errors.New("routing table needs points")
	}
	coords := make([]string, len(points))
	for i, p := range points {
		coords[i] = strconv.FormatFloat(p.Longitude, 'f', 6, 64) + "," + strconv.FormatFloat(p.Latitude, 'f', 6, 64)
	}
	target := strings.TrimRight(endpoint, "/") + "/table/v1/" + profile + "/" + strings.Join(coords, ";") + "?annotations=duration,distance&generate_hints=false"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Table{}, fmt.Errorf("routing table request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Table{}, fmt.Errorf("routing table request: %w", err)
	}
	defer resp.Body.Close()

	var body tableResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTableBytes)).Decode(&body); err != nil {
		return Table{}, fmt.Errorf("decode routing table (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || body.Code != "Ok" {
		return Table{}, fmt.Errorf("routing table failed with status %d: %s: %s", resp.StatusCode, body.Code, body.Message)
	}
	if body.DataVersion == "" {
		return Table{}, errors.New("routing graph has no data version")
	}
	n := len(points)
	if len(body.Durations) != n || len(body.Distances) != n || len(body.Sources) != n {
		return Table{}, errors.New("routing table size does not match the request")
	}
	table := Table{Version: body.DataVersion, size: n, SnapMeters: make([]float64, n)}
	for i := range n {
		if len(body.Durations[i]) != n || len(body.Distances[i]) != n {
			return Table{}, errors.New("routing table size does not match the request")
		}
		table.seconds = append(table.seconds, body.Durations[i]...)
		table.meters = append(table.meters, body.Distances[i]...)
		table.SnapMeters[i] = body.Sources[i].Distance
	}
	return table, nil
}

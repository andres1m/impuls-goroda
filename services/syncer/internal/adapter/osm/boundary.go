package osm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

// The same administrative relations bound the areas the place query searches.
var boundaryRelations = map[domain.City]string{
	domain.Moscow: `rel["name"="Москва"]["admin_level"="4"]["boundary"="administrative"]`,
	domain.Perm:   `rel["name"="Пермский городской округ"]["boundary"="administrative"]`,
}

// CityBoundary is an administrative relation as lines of [lon, lat] points, the order GeoJSON uses.
type CityBoundary struct {
	RelationID int64
	OSMBase    string
	Lines      [][][2]float64
}

type boundaryResponse struct {
	OSM3S struct {
		TimestampOSMBase string `json:"timestamp_osm_base"`
	} `json:"osm3s"`
	Remark   string `json:"remark"`
	Elements []struct {
		ID      int64 `json:"id"`
		Members []struct {
			Type     string `json:"type"`
			Geometry []struct {
				Lat float64 `json:"lat"`
				Lon float64 `json:"lon"`
			} `json:"geometry"`
		} `json:"members"`
	} `json:"elements"`
}

// Boundary fetches the city's administrative relation with the geometry of its ways.
func (a *Adapter) Boundary(ctx context.Context, city domain.City) (CityBoundary, error) {
	for attempt := 0; ; attempt++ {
		b, err := a.fetchBoundary(ctx, city)
		if err == nil || !retryable(err) || attempt == len(a.retryDelays) {
			return b, err
		}
		select {
		case <-ctx.Done():
			return CityBoundary{}, fmt.Errorf("fetch boundary of %s: %w", city, ctx.Err())
		case <-time.After(a.retryDelays[attempt]):
		}
	}
}

func retryable(err error) bool {
	var fetchErr *ingest.FetchError
	if !errors.As(err, &fetchErr) {
		return false
	}
	switch fetchErr.Code {
	case "transport", "decode", "overpass_remark", "http_status_429":
		return true
	}
	return strings.HasPrefix(fetchErr.Code, "http_status_5")
}

func (a *Adapter) fetchBoundary(ctx context.Context, city domain.City) (CityBoundary, error) {
	selector, ok := boundaryRelations[city]
	if !ok {
		return CityBoundary{}, &ingest.FetchError{Code: "unknown_city", Err: fmt.Errorf("no boundary relation for %s", city)}
	}
	var body boundaryResponse
	if err := a.post(ctx, "[out:json][timeout:180];"+selector+";out geom;", &body); err != nil {
		return CityBoundary{}, err
	}
	if body.Remark != "" {
		return CityBoundary{}, &ingest.FetchError{Code: "overpass_remark", Err: errors.New(body.Remark)}
	}
	if len(body.Elements) != 1 {
		return CityBoundary{}, &ingest.FetchError{Code: "boundary_relations",
			Err: fmt.Errorf("%d relations match the boundary of %s", len(body.Elements), city)}
	}
	relation := body.Elements[0]
	b := CityBoundary{RelationID: relation.ID, OSMBase: body.OSM3S.TimestampOSMBase}
	for _, member := range relation.Members {
		if member.Type != "way" || len(member.Geometry) < 2 {
			continue
		}
		line := make([][2]float64, len(member.Geometry))
		for i, p := range member.Geometry {
			line[i] = [2]float64{p.Lon, p.Lat}
		}
		b.Lines = append(b.Lines, line)
	}
	if len(b.Lines) == 0 {
		return CityBoundary{}, &ingest.FetchError{Code: "boundary_without_lines",
			Err: fmt.Errorf("relation %d has no ways", relation.ID)}
	}
	return b, nil
}

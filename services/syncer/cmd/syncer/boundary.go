package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/adapter/osm"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/repo/postgres"
)

// The relation query asks Overpass for up to 180 s of work; the client waits a little longer.
const boundaryRequestTimeout = 200 * time.Second

func parseBoundaryArgs(args []string) ([]domain.City, error) {
	usage := errors.New("usage: syncer boundary [moscow|perm]")
	switch len(args) {
	case 0:
		return []domain.City{domain.Moscow, domain.Perm}, nil
	case 1:
		city, err := domain.ParseCity(args[0])
		if err != nil {
			return nil, errors.Join(err, usage)
		}
		return []domain.City{city}, nil
	}
	return nil, usage
}

// runBoundary loads the cities' administrative boundaries, which materialization checks places against.
func runBoundary(ctx context.Context, args []string) (resultErr error) {
	cities, err := parseBoundaryArgs(args)
	if err != nil {
		return err
	}
	database, err := openDatabase(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Stop(context.Background())) }()
	adapter := osm.New(osm.DefaultBaseURL, &http.Client{Timeout: boundaryRequestTimeout}, time.Now)
	for _, city := range cities {
		b, err := adapter.Boundary(ctx, city)
		if err != nil {
			return fmt.Errorf("%s: %w", city, err)
		}
		stats, err := postgres.SaveBoundary(ctx, database.Pool, city, b.Lines, time.Now().UTC())
		if err != nil {
			return err
		}
		fmt.Printf("%s: relation %d, osm base %s, %d parts, %.0f km2\n", city, b.RelationID, b.OSMBase, stats.Parts, stats.AreaKm2)
	}
	return nil
}

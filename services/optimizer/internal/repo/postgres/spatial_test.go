package postgres

import (
	"context"
	"math"
	"testing"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

var permCenter = domain.Coordinate{Longitude: 56.25, Latitude: 58.0}

func TestNearbyPOIsRejectsInvalidInput(t *testing.T) {
	s := NewSpatial(nil)
	cases := map[string]struct {
		city   string
		center domain.Coordinate
		radius float64
	}{
		"blank city":      {" ", permCenter, 300},
		"bad center":      {"perm", domain.Coordinate{Longitude: 56.25, Latitude: 91}, 300},
		"zero radius":     {"perm", permCenter, 0},
		"negative radius": {"perm", permCenter, -1},
		"nan radius":      {"perm", permCenter, math.NaN()},
		"infinite radius": {"perm", permCenter, math.Inf(1)},
	}
	for name, tc := range cases {
		if _, err := s.NearbyPOIs(context.Background(), tc.city, tc.center, tc.radius); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCityCoversRejectsInvalidInput(t *testing.T) {
	s := NewSpatial(nil)
	if _, _, err := s.CityCovers(context.Background(), "", permCenter); err == nil {
		t.Error("blank city accepted")
	}
	if _, _, err := s.CityCovers(
		context.Background(),
		"perm",
		domain.Coordinate{Longitude: 181, Latitude: 58},
	); err == nil {
		t.Error("bad point accepted")
	}
}

package normalize

import (
	"math"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

type point struct{ lat, lon float64 }

var cityCenters = map[domain.City]point{
	domain.Moscow: {55.7558, 37.6173},
	domain.Perm:   {58.0105, 56.2502},
}

var cityNames = map[domain.City]string{domain.Moscow: "Москва", domain.Perm: "Пермь"}

// A point farther than this from the city centre is taken as a data error; the precise city boundary
// check belongs to quarantine.
const cityRadiusKm = 80
const halfCircleDegrees = 180
const haversineFactor = 2

// cityPoint reads a coordinate pair whose order sources do not keep and takes the order that lands in
// the city; when both do, the nearer one.
func cityPoint(city domain.City, a, b float64) (lat, lon float64, ok bool) {
	center, known := cityCenters[city]
	if !known {
		return 0, 0, false
	}
	best := math.Inf(1)
	for _, p := range []point{{a, b}, {b, a}} {
		if p.lat < -90 || p.lat > 90 || p.lon < -halfCircleDegrees || p.lon > halfCircleDegrees {
			continue
		}
		if d := distanceKm(center, p); d <= cityRadiusKm && d < best {
			lat, lon, best, ok = p.lat, p.lon, d, true
		}
	}
	return lat, lon, ok
}

func distanceKm(a, b point) float64 {
	const earthRadiusKm = 6371
	rad := math.Pi / halfCircleDegrees
	dLat, dLon := (b.lat-a.lat)*rad, (b.lon-a.lon)*rad
	h := math.Sin(dLat/haversineFactor)*math.Sin(dLat/haversineFactor) +
		math.Cos(a.lat*rad)*math.Cos(b.lat*rad)*math.Sin(dLon/haversineFactor)*math.Sin(dLon/haversineFactor)
	return haversineFactor * earthRadiusKm * math.Asin(math.Sqrt(h))
}

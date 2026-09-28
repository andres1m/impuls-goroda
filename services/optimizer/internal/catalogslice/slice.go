// Package catalogslice holds the catalog of one city as one revision published it, and turns it into
// the candidates of a request without going back to the database.
package catalogslice

import (
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Place is an active place with what its visits need besides the request's interval.
type Place struct {
	Place     domain.Place
	Rules     domain.OpeningRules
	BaseScore float64
	Entrances []domain.Entrance
}

type Slice struct {
	City      string
	Timezone  string
	Revision  domain.CatalogRevision
	UpdatedAt time.Time
	BuiltAt   time.Time
	// Sessions that ended by then are left out; a request starting earlier needs its own slice.
	Horizon time.Time
	// By place id.
	Places []Place
	// Session candidates, cancelled and sold out ones included, by start and id.
	Sessions []domain.Candidate
}

func (s *Slice) PlaceByID(id domain.PlaceID) (Place, bool) {
	for _, p := range s.Places {
		if p.Place.ID == id {
			return p, true
		}
	}
	return Place{}, false
}

// Covers tells whether every session the request's day may use is in the slice.
func (s *Slice) Covers(req domain.OptimizeRequest) bool {
	return !req.Start.Before(s.Horizon)
}

// Missing lists the sessions the request's obligations name that the slice does not hold.
func (s *Slice) Missing(req domain.OptimizeRequest) []domain.SessionID {
	var missing []domain.SessionID
	for id := range obligated(req) {
		if s.session(id) < 0 {
			missing = append(missing, id)
		}
	}
	return missing
}

func (s *Slice) session(id domain.SessionID) int {
	for i, c := range s.Sessions {
		if c.Session.ID == id {
			return i
		}
	}
	return -1
}

func obligated(req domain.OptimizeRequest) map[domain.SessionID]struct{} {
	set := make(map[domain.SessionID]struct{}, len(req.Constraints.Obligations))
	for _, o := range req.Constraints.Obligations {
		if o.SessionID != nil {
			set[*o.SessionID] = struct{}{}
		}
	}
	return set
}

// Rough bytes per item, for the cache to weigh slices against each other and its memory limit.
const (
	sliceBytes    = 512
	placeBytes    = 1024
	entranceBytes = 128
	sessionBytes  = 2048
	offerBytes    = 256
)

// Size estimates the memory the slice holds.
func (s *Slice) Size() int64 {
	size := int64(sliceBytes)
	for _, p := range s.Places {
		size += placeBytes + int64(len(p.Entrances))*entranceBytes
	}
	for _, c := range s.Sessions {
		size += sessionBytes + int64(len(c.Offers))*offerBytes + int64(len(c.Entrances))*entranceBytes
	}
	return size
}

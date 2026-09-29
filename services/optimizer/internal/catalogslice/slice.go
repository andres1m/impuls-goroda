// Package catalogslice holds the catalog of one city as one revision published it, and turns it into
// the candidates of a request without going back to the database.
package catalogslice

import (
	"time"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

// Place is an active place with what its visits need besides the request's interval.
type Place struct {
	Place     domain.Place        `json:"Place"`
	Rules     domain.OpeningRules `json:"Rules"`
	BaseScore float64             `json:"BaseScore"`
	Entrances []domain.Entrance   `json:"Entrances"`
}

type Slice struct {
	City      string                 `json:"City"`
	Timezone  string                 `json:"Timezone"`
	Revision  domain.CatalogRevision `json:"Revision"`
	UpdatedAt time.Time              `json:"UpdatedAt"`
	BuiltAt   time.Time              `json:"BuiltAt"`
	// Sessions that ended by then are left out; a request starting earlier needs its own slice.
	Horizon time.Time `json:"Horizon"`
	// By place id.
	Places []Place `json:"Places"`
	// Session candidates, cancelled and sold out ones included, by start and id.
	Sessions []domain.Candidate `json:"Sessions"`
}

// Covers tells whether every session the request's day may use is in the slice.
func (s *Slice) Covers(req *domain.OptimizeRequest) bool {
	return !req.Start.Before(s.Horizon)
}

// Missing lists the sessions the request's obligations name that the slice does not hold.
func (s *Slice) Missing(req *domain.OptimizeRequest) []domain.SessionID {
	wanted := obligated(req)
	for i := range s.Sessions {
		delete(wanted, s.Sessions[i].Session.ID)
	}
	missing := make([]domain.SessionID, 0, len(wanted))
	for id := range wanted {
		missing = append(missing, id)
	}
	return missing
}

func obligated(req *domain.OptimizeRequest) map[domain.SessionID]struct{} {
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
	for i := range s.Places {
		size += placeBytes + int64(len(s.Places[i].Entrances))*entranceBytes
	}
	for i := range s.Sessions {
		c := &s.Sessions[i]
		size += sessionBytes + int64(len(c.Offers))*offerBytes + int64(len(c.Entrances))*entranceBytes
	}
	return size
}

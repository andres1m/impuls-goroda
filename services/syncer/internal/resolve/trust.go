package resolve

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

const (
	AttrTitle         = "title"
	AttrCoordinates   = "coordinates"
	AttrAddress       = "address"
	AttrCategory      = "category"
	AttrTags          = "tags"
	AttrOpeningRules  = "opening_rules"
	AttrSourcePlaceID = "source_place_id"
)

// Trust lists, per attribute, the sources whose value wins, most trusted first.
type Trust map[string][]domain.SourceKey

var DefaultTrust = Trust{
	AttrCoordinates:  {domain.OSM, domain.MkrfEvents, domain.KudaGo},
	AttrTitle:        {domain.MkrfEvents, domain.OSM, domain.KudaGo},
	AttrAddress:      {domain.MkrfEvents, domain.OSM, domain.KudaGo},
	AttrCategory:     {domain.MkrfEvents, domain.OSM, domain.KudaGo},
	AttrTags:         {domain.MkrfEvents, domain.OSM, domain.KudaGo},
	AttrOpeningRules: {domain.KudaGo, domain.MkrfEvents, domain.OSM},
}

// Rank is 1 for the most trusted source; a source outside the list ranks last. The source place key
// is provenance and has no rank.
func (t Trust) Rank(attribute string, source domain.SourceKey) int {
	if attribute == AttrSourcePlaceID {
		return 0
	}
	order := t[attribute]
	if i := slices.Index(order, source); i >= 0 {
		return i + 1
	}
	return len(order) + 1
}

type Fact struct {
	ID             string
	Attribute      string
	Source         domain.SourceKey
	Value          json.RawMessage
	FetchedAt      time.Time
	DataMode       domain.DataMode
	SourceRecordID string
}

// Select picks the fact each attribute takes its value from: the most trusted source, then the newest
// fetch, then the record the place already follows (several records of one source often describe a
// place a little differently, and processing them again must not move it), then the smallest ID so
// that the choice does not depend on the order facts come in.
func (t Trust) Select(facts []Fact, incumbent string) map[string]Fact {
	best := make(map[string]Fact)
	for i := range facts {
		f := facts[i]
		if f.Attribute == AttrSourcePlaceID {
			continue
		}
		cur, ok := best[f.Attribute]
		if !ok || t.better(f, cur, incumbent) {
			best[f.Attribute] = f
		}
	}
	return best
}

func (t Trust) better(a, b Fact, incumbent string) bool {
	if ra, rb := t.Rank(a.Attribute, a.Source), t.Rank(b.Attribute, b.Source); ra != rb {
		return ra < rb
	}
	if !a.FetchedAt.Equal(b.FetchedAt) {
		return a.FetchedAt.After(b.FetchedAt)
	}
	if ai, bi := a.SourceRecordID == incumbent, b.SourceRecordID == incumbent; ai != bi {
		return ai
	}
	return a.ID < b.ID
}

var modeOrder = []domain.DataMode{domain.Synthetic, domain.Prepared, domain.Live}

// LeastTrusted is the mode a row honestly carries when its values come from several records.
func LeastTrusted(modes ...domain.DataMode) domain.DataMode {
	least := domain.Live
	for _, m := range modes {
		if slices.Index(modeOrder, m) < slices.Index(modeOrder, least) {
			least = m
		}
	}
	return least
}

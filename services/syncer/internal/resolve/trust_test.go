package resolve

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

func TestDefaultTrustOrders(t *testing.T) {
	for _, c := range []struct {
		attr   string
		source domain.SourceKey
		rank   int
	}{
		{AttrCoordinates, domain.OSM, 1}, {AttrCoordinates, domain.MkrfEvents, 2}, {AttrCoordinates, domain.KudaGo, 3},
		{AttrTitle, domain.MkrfEvents, 1}, {AttrTitle, domain.OSM, 2}, {AttrTitle, domain.KudaGo, 3},
		{AttrAddress, domain.MkrfEvents, 1}, {AttrCategory, domain.OSM, 2}, {AttrTags, domain.KudaGo, 3},
		{AttrOpeningRules, domain.KudaGo, 1}, {AttrOpeningRules, domain.MkrfEvents, 2}, {AttrOpeningRules, domain.OSM, 3},
		{AttrTitle, "unknown_source", 4},
		{AttrSourcePlaceID, domain.OSM, 0},
	} {
		if got := DefaultTrust.Rank(c.attr, c.source); got != c.rank {
			t.Errorf("Rank(%s, %s) = %d, want %d", c.attr, c.source, got, c.rank)
		}
	}
}

func fact(id, attr string, source domain.SourceKey, value string, at time.Time) Fact {
	return Fact{ID: id, Attribute: attr, Source: source, Value: json.RawMessage(value), FetchedAt: at, DataMode: domain.Live}
}

func TestSelectPrefersTheMoreTrustedSourceThenTheNewer(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	facts := []Fact{
		fact("a", AttrTitle, domain.OSM, `"osm name"`, t0.Add(time.Hour)),
		fact("b", AttrTitle, domain.MkrfEvents, `"mkrf old"`, t0),
		fact("c", AttrTitle, domain.MkrfEvents, `"mkrf new"`, t0.Add(time.Minute)),
		fact("d", AttrCoordinates, domain.OSM, `{"lat":1,"lon":2}`, t0),
		fact("e", AttrCoordinates, domain.MkrfEvents, `{"lat":3,"lon":4}`, t0.Add(time.Hour)),
		fact("f", AttrSourcePlaceID, domain.OSM, `"node/1"`, t0),
	}
	got := DefaultTrust.Select(facts, "")
	if got[AttrTitle].ID != "c" || got[AttrCoordinates].ID != "d" {
		t.Fatalf("selected %+v", got)
	}
	if _, ok := got[AttrSourcePlaceID]; ok {
		t.Fatal("the source place key is provenance, never a value")
	}
}

func TestSelectBreaksFullTiesByFactID(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	a := DefaultTrust.Select([]Fact{fact("x", AttrTitle, domain.OSM, `"1"`, t0), fact("y", AttrTitle, domain.OSM, `"2"`, t0)}, "")
	b := DefaultTrust.Select([]Fact{fact("y", AttrTitle, domain.OSM, `"2"`, t0), fact("x", AttrTitle, domain.OSM, `"1"`, t0)}, "")
	if a[AttrTitle].ID != b[AttrTitle].ID {
		t.Fatal("selection depends on the order facts are listed in")
	}
}

func TestSelectKeepsTheIncumbentRecordOnATie(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	x := fact("x", AttrAddress, domain.MkrfEvents, `"ул. Ленина, 1"`, t0)
	y := fact("y", AttrAddress, domain.MkrfEvents, `"ул Ленина,д 1"`, t0)
	x.SourceRecordID, y.SourceRecordID = "rec-x", "rec-y"
	if got := DefaultTrust.Select([]Fact{x, y}, "rec-y")[AttrAddress]; got.ID != "y" {
		t.Fatalf("a tie must not move the place off the record it already follows: %+v", got)
	}
	if got := DefaultTrust.Select([]Fact{x, y}, "rec-x")[AttrAddress]; got.ID != "x" {
		t.Fatalf("selected %+v", got)
	}
	newer := fact("z", AttrAddress, domain.MkrfEvents, `"новый адрес"`, t0.Add(time.Minute))
	newer.SourceRecordID = "rec-z"
	if got := DefaultTrust.Select([]Fact{x, y, newer}, "rec-y")[AttrAddress]; got.ID != "z" {
		t.Fatalf("newer data still wins: %+v", got)
	}
}

func TestLeastTrusted(t *testing.T) {
	if LeastTrusted(domain.Live, domain.Prepared) != domain.Prepared ||
		LeastTrusted(domain.Live, domain.Prepared, domain.Synthetic) != domain.Synthetic ||
		LeastTrusted(domain.Live) != domain.Live {
		t.Fatal("least trusted mode is synthetic < prepared < live")
	}
}

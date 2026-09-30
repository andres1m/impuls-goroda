// Package coverage describes what the catalog holds per city and how fresh each part of it is.
package coverage

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
)

// Source is one source's collection state for a city. The times are kept apart on purpose: FetchedAt is
// when the syncer received the data, SourceUpdatedAt is what the source itself reports, if it reports
// anything.
type Source struct {
	Key                   domain.SourceKey
	City                  domain.City
	DataModes             string
	LastAttemptAt         *time.Time
	LastSuccessAt         *time.Time
	LastErrorCode         string
	PublishedTo           *time.Time
	MaterializedTo        *time.Time
	Pending, Applied      int
	Failed, Quarantined   int
	LatestFetchedAt       *time.Time
	LatestSourceUpdatedAt *time.Time
}

// Attribute is the freshness of one attribute of catalog entities, over the facts currently selected.
// VerifiedAt is set only for facts somebody verified by hand.
type Attribute struct {
	City                  domain.City
	Target                string
	Name                  string
	Facts                 int
	OldestFetchedAt       time.Time
	LatestFetchedAt       time.Time
	LatestSourceUpdatedAt *time.Time
	Verified              int
	LatestVerifiedAt      *time.Time
}

// Catalog counts the active catalog rows of a city by the data mode they are shown in. The catalog keeps
// no time of the last availability check: LatestStatusChangeAt is when a session last changed its status.
type Catalog struct {
	City                 domain.City
	DataMode             domain.DataMode
	Places, Events       int
	UpcomingSessions     int
	Cancelled            int
	AvailabilityUnknown  int
	AvailabilityReported int
	LatestStatusChangeAt *time.Time
}

type Report struct {
	Sources    []Source
	Attributes []Attribute
	Catalog    []Catalog
}

func stamp(t *time.Time, now time.Time) string {
	if t == nil {
		return "-"
	}
	return fmt.Sprintf("%s (%s ago)", t.UTC().Format("2006-01-02 15:04"), now.Sub(*t).Round(time.Minute))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Render prints the report as text. It states ages but never calls any of them fresh or stale: no single
// age limit fits descriptions, prices, registration and seat availability alike.
func Render(w io.Writer, r *Report, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "sources: fetched = received by the syncer; source updated = reported by the source, may be absent")
	fmt.Fprintln(tw, "source\tcity\tdata mode\tlast attempt\tlast success\tlast error\t"+
		"raw pending/applied/failed/quarantined\tlatest fetched\tlatest source updated\tpublished to\tmaterialized to")
	for i := range r.Sources {
		s := &r.Sources[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d/%d/%d/%d\t%s\t%s\t%s\t%s\n", s.Key, s.City, orDash(s.DataModes),
			stamp(s.LastAttemptAt, now), stamp(s.LastSuccessAt, now), orDash(s.LastErrorCode),
			s.Pending, s.Applied, s.Failed, s.Quarantined,
			stamp(s.LatestFetchedAt, now), stamp(s.LatestSourceUpdatedAt, now),
			stamp(s.PublishedTo, now), stamp(s.MaterializedTo, now))
	}
	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "attributes (selected facts): verified = checked by hand, empty unless somebody did")
	fmt.Fprintln(tw, "city\tentity\tattribute\tfacts\toldest fetched\tlatest fetched\t"+
		"latest source updated\tverified\tlatest verified")
	for i := range r.Attributes {
		a := &r.Attributes[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s\n", a.City, a.Target, a.Name, a.Facts,
			stamp(&a.OldestFetchedAt, now), stamp(&a.LatestFetchedAt, now), stamp(a.LatestSourceUpdatedAt, now),
			a.Verified, stamp(a.LatestVerifiedAt, now))
	}
	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "catalog (active rows by data mode; sessions of active events that have not ended yet)")
	fmt.Fprintln(tw, "city\tdata mode\tplaces\tevents\tsessions\tcancelled\tavailability unknown\t"+
		"availability reported\tstatus last changed")
	for i := range r.Catalog {
		c := &r.Catalog[i]
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", c.City, c.DataMode, c.Places, c.Events,
			c.UpcomingSessions, c.Cancelled, c.AvailabilityUnknown, c.AvailabilityReported,
			stamp(c.LatestStatusChangeAt, now))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render coverage: %w", err)
	}
	return nil
}

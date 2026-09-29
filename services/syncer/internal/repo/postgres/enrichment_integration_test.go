package postgres

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/enrich"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/normalize"
)

func (f *materializeFixture) tagMasks(t *testing.T, id string) (place, poi int64) {
	t.Helper()
	err := f.pool.QueryRow(f.ctx, `
		SELECT p.tag_mask::bigint, l.tag_mask::bigint FROM catalog.place p
		JOIN catalog.leisure_poi l ON l.id = p.id AND l.city = p.city
		WHERE p.id = $1 AND p.city = 'perm'`, id).Scan(&place, &poi)
	if err != nil {
		t.Fatal(err)
	}
	return place, poi
}

func TestEnrichmentStoreIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	title := "Кофейня без тегов " + randomSuffix(t)
	raw := f.save(t, "node/e1", `{"v":1}`, base)
	raws := f.pending(t, raw.ID)
	outcome := &materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: raws[0], Place: draft("node/e1", "gastro", title)},
	}}
	if _, _, err := f.store.Publish(f.ctx, domain.Perm, outcome, base); err != nil {
		t.Fatal(err)
	}

	store := NewEnrichmentStore(f.pool)
	vocab, err := store.Vocabulary(f.ctx)
	if err != nil || len(vocab) != 13 || vocab[0].Code != "contemporary_art" || vocab[0].Bit != 0 {
		t.Fatalf("vocabulary %+v %v", vocab, err)
	}
	cands, err := store.Candidates(f.ctx, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	var mine *enrich.Candidate
	for i := range cands {
		if cands[i].Title == title {
			mine = &cands[i]
		}
	}
	if mine == nil || mine.Place == nil || mine.HasTags || mine.StoredHash != nil || mine.Category != "Гастрономия" {
		t.Fatalf("candidate %+v", mine)
	}
	id := mine.Place.String()

	before := f.revision(t)
	done := []enrich.Enriched{{Candidate: *mine, Hash: enrich.Hash(enrich.Input(mine)), Mask: 1<<7 | 1<<1}}
	published, err := store.Publish(f.ctx, domain.Perm, "test/model", done, base.Add(time.Minute))
	if err != nil || !published || f.revision(t) != before+1 {
		t.Fatalf("published %v err %v revision %d -> %d", published, err, before, f.revision(t))
	}
	if place, poi := f.tagMasks(t, id); place != 1<<7|1<<1 || poi != place {
		t.Fatalf("masks place %b poi %b", place, poi)
	}

	again, err := store.Publish(f.ctx, domain.Perm, "test/model", done, base.Add(2*time.Minute))
	if err != nil || again || f.revision(t) != before+1 {
		t.Fatalf("second publish %v %v revision %d", again, err, f.revision(t))
	}
	cands, err = store.Candidates(f.ctx, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cands {
		if cands[i].Place != nil && cands[i].Place.String() == id &&
			(string(cands[i].StoredHash) != string(done[0].Hash) || !cands[i].HasTags) {
			t.Fatalf("candidate after publish %+v", cands[i])
		}
	}

	var announced int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM integration.change_delivery
		WHERE city = 'perm' AND catalog_revision = $1 AND destination = 'redis'`, before+1).Scan(&announced); err != nil || announced != 1 {
		t.Fatalf("revision announcements %d %v", announced, err)
	}
}

func TestMaterializationKeepsLLMTagsIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	base := time.Now().UTC().Truncate(time.Microsecond)
	title := "Кофейня " + randomSuffix(t)
	raw := f.save(t, "node/k1", `{"v":1}`, base)
	publish := func(raw materialize.Raw, title string, at time.Time, tags ...string) {
		t.Helper()
		outcome := &materialize.Outcome{Apply: []materialize.Normalized{
			{Raw: f.pending(t, raw.ID)[0], Place: draft("node/k1", "gastro", title, tags...)},
		}}
		if _, _, err := f.store.Publish(f.ctx, domain.Perm, outcome, at); err != nil {
			t.Fatal(err)
		}
	}
	publish(raw, title, base, "gastro_coffee")

	store := NewEnrichmentStore(f.pool)
	cands, err := store.Candidates(f.ctx, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	var mine enrich.Candidate
	for i := range cands {
		if cands[i].Title == title {
			mine = cands[i]
		}
	}
	done := []enrich.Enriched{{Candidate: mine, Hash: enrich.Hash(enrich.Input(&mine)), Mask: 1 << 1}}
	if _, err := store.Publish(f.ctx, domain.Perm, "test/model", done, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	id := mine.Place.String()

	next := f.save(t, "node/k1", `{"v":2}`, base.Add(2*time.Minute))
	publish(next, title+" на набережной", base.Add(2*time.Minute), "gastro_coffee")
	if place, poi := f.tagMasks(t, id); place != 1<<7|1<<1 || poi != place {
		t.Fatalf("masks after rematerialization: place %b poi %b", place, poi)
	}
}

func TestEnrichmentOfAnEventKeepsAcrossRematerializationIntegration(t *testing.T) {
	f := newMaterializeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	day := now.Add(24 * time.Hour)
	eventID := normalize.EntityID(string(f.source) + ":event:1@place:a")
	placeID := normalize.EntityID(string(f.source) + ":place:a")
	if !f.publishEvent(t, 1, now, "a", day) {
		t.Fatal("event did not publish")
	}

	store := NewEnrichmentStore(f.pool)
	cands, err := store.Candidates(f.ctx, domain.Perm)
	if err != nil {
		t.Fatal(err)
	}
	var mine enrich.Candidate
	for i := range cands {
		if cands[i].Event != nil && *cands[i].Event == eventID {
			mine = cands[i]
		}
	}
	if mine.Event == nil || !mine.HasTags || mine.Category != "Культура" {
		t.Fatalf("event candidate %+v", mine)
	}

	before := f.revision(t)
	done := []enrich.Enriched{{Candidate: mine, Hash: enrich.Hash(enrich.Input(&mine)), Mask: 1 << 8}}
	published, err := store.Publish(f.ctx, domain.Perm, "test/model", done, now.Add(time.Minute))
	if err != nil || !published || f.revision(t) != before+1 {
		t.Fatalf("published %v err %v revision %d -> %d", published, err, before, f.revision(t))
	}
	const want = 1<<11 | 1<<8
	var eventMask int64
	if err := f.pool.QueryRow(f.ctx, `SELECT tag_mask::bigint FROM catalog.event WHERE city = 'perm' AND id = $1`,
		eventID).Scan(&eventMask); err != nil || eventMask != want {
		t.Fatalf("event mask %b %v", eventMask, err)
	}
	if _, poi := f.tagMasks(t, placeID.String()); poi != want {
		t.Fatalf("projection mask %b", poi)
	}

	revision := f.revision(t)
	if f.publishEvent(t, 2, now, "a", day) || f.revision(t) != revision {
		t.Fatal("unchanged content rewrote the enriched event")
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT tag_mask::bigint FROM catalog.event WHERE city = 'perm' AND id = $1`,
		eventID).Scan(&eventMask); err != nil || eventMask != want {
		t.Fatalf("event mask after rematerialization %b %v", eventMask, err)
	}
}

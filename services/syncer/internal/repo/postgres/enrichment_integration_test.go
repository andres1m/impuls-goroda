package postgres

import (
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/enrich"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
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
	raw := f.save(t, "node/e1", `{"v":1}`, base)
	raws := f.pending(t, raw.ID)
	outcome := &materialize.Outcome{Apply: []materialize.Normalized{
		{Raw: raws[0], Place: draft("node/e1", "gastro", "Кофейня без тегов")},
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
		if cands[i].Title == "Кофейня без тегов" {
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

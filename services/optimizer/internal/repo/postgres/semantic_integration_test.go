package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/andres1m/impuls-goroda/pkg/ai"
	"github.com/andres1m/impuls-goroda/services/optimizer/internal/domain"
)

func axis(i int) []float32 {
	v := make([]float32, 384)
	v[i] = 1
	return v
}

func insertEntityEmbedding(ctx context.Context, t *testing.T, tx pgx.Tx, city string, place *domain.PlaceID, event *domain.EventID, session *domain.SessionID, space ai.Space, vector []float32) {
	t.Helper()
	_, err := tx.Exec(ctx, `
		INSERT INTO catalog.entity_embedding (id, city, place_id, event_id, session_id, model_key, model_version, embedding, content_hash, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7::vector, '\x00', now())`,
		city, place, event, session, space.Key, space.Version, vectorLiteral(vector))
	if err != nil {
		t.Fatal(err)
	}
}

func insertEventFixture(ctx context.Context, t *testing.T, tx pgx.Tx, city string, place domain.PlaceID, active bool) domain.EventID {
	t.Helper()
	id := randomBytes16[domain.EventID](t)
	_, err := tx.Exec(ctx, `
		INSERT INTO catalog.event (id, city, place_id, title, normalized_title, category, tag_mask, data_mode, is_active, review_required, created_at, updated_at)
		VALUES ($1, $2, $3, 'Fixture Event', 'fixture event', 'culture', 0::bit(64), 'synthetic', $4, false, now(), now())`,
		id, city, place, active)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertSessionFixture(ctx context.Context, t *testing.T, tx pgx.Tx, city string, event domain.EventID, status domain.Availability) domain.SessionID {
	t.Helper()
	id := randomBytes16[domain.SessionID](t)
	_, err := tx.Exec(ctx, `
		INSERT INTO catalog.session (id, city, event_id, slot_type, starts_at, ends_at, min_duration_s, recommended_duration_s, buffer_s, access_type, availability_status, is_hard_constraint, data_mode, version, updated_at)
		VALUES ($1, $2, $3, 'FIXED_SESSION', now(), now() + interval '1 hour', 3600, 3600, 0, 'ticket', $4, true, 'synthetic', 1, now())`,
		id, city, event, string(status))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNearestEntitiesIntegration(t *testing.T) {
	ctx, tx := fixtureTx(t)
	perm := domain.Coordinate{Longitude: 56.25, Latitude: 58.0}
	moscow := domain.Coordinate{Longitude: 37.62, Latitude: 55.75}
	space := ai.Space{Key: "test/nearest-" + t.Name(), Version: "d384"}
	other := ai.Space{Key: space.Key, Version: "d385"}

	near := insertPOI(ctx, t, tx, "perm", perm, 10, 0, true)
	far := insertPOI(ctx, t, tx, "perm", perm, 20, 0, true)
	inactivePlace := insertPOI(ctx, t, tx, "perm", perm, 5, 0, false)
	elsewhere := insertPOI(ctx, t, tx, "moscow", moscow, 10, 0, true)
	otherModel := insertPOI(ctx, t, tx, "perm", perm, 30, 0, true)

	activeEvent := insertEventFixture(ctx, t, tx, "perm", near, true)
	inactiveEvent := insertEventFixture(ctx, t, tx, "perm", near, false)
	eventAtInactivePlace := insertEventFixture(ctx, t, tx, "perm", inactivePlace, true)

	activeSession := insertSessionFixture(ctx, t, tx, "perm", activeEvent, domain.AvailabilityAvailable)
	cancelledSession := insertSessionFixture(ctx, t, tx, "perm", activeEvent, domain.AvailabilityCancelled)
	soldOutSession := insertSessionFixture(ctx, t, tx, "perm", activeEvent, domain.AvailabilitySoldOut)
	sessionOfInactiveEvent := insertSessionFixture(ctx, t, tx, "perm", inactiveEvent, domain.AvailabilityAvailable)

	query := axis(0)
	query[1] = 0.2
	query[2] = 0.1

	// Give every inactive/unavailable entity an exact match (axis(0)) so an unfiltered LIMIT would pick them first.
	insertEntityEmbedding(ctx, t, tx, "perm", &inactivePlace, nil, nil, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, &inactiveEvent, nil, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, &eventAtInactivePlace, nil, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, nil, &cancelledSession, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, nil, &soldOutSession, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, nil, &sessionOfInactiveEvent, space, axis(0))

	insertEntityEmbedding(ctx, t, tx, "perm", &near, nil, nil, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, &activeEvent, nil, space, axis(1))
	insertEntityEmbedding(ctx, t, tx, "perm", nil, nil, &activeSession, space, axis(2))
	insertEntityEmbedding(ctx, t, tx, "perm", &far, nil, nil, space, axis(3))
	insertEntityEmbedding(ctx, t, tx, "moscow", &elsewhere, nil, nil, space, axis(0))
	insertEntityEmbedding(ctx, t, tx, "perm", &otherModel, nil, nil, other, axis(0))

	if _, err := tx.Exec(ctx, `SET LOCAL ROLE impuls_optimizer`); err != nil {
		t.Fatal(err)
	}

	got, err := NewCatalog(savepointDB{tx}).NearestEntities(ctx, "perm", space, query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 ||
		got[0].Place == nil || *got[0].Place != near ||
		got[1].Event == nil || *got[1].Event != activeEvent ||
		got[2].Session == nil || *got[2].Session != activeSession ||
		got[3].Place == nil || *got[3].Place != far {
		t.Fatalf("matches %+v", got)
	}

	limited, err := NewCatalog(savepointDB{tx}).NearestEntities(ctx, "perm", space, query, 1)
	if err != nil || len(limited) != 1 || limited[0].Place == nil || *limited[0].Place != near {
		t.Fatalf("limited %+v, %v", limited, err)
	}

	none, err := NewCatalog(savepointDB{tx}).NearestEntities(ctx, "perm", ai.Space{Key: "test/absent", Version: "d384"}, query, 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("absent space %+v, %v", none, err)
	}
}

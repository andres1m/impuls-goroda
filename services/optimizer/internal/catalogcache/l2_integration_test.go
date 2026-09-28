package catalogcache

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/repo/postgres"
)

type poolDB struct{ *pgxpool.Pool }

func (p poolDB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return p.Pool.BeginTx(ctx, opts)
}

// Every city's real catalog comes back from the shared cache exactly as the database gave it.
func TestRealSlicesSurviveTheSharedCacheIntegration(t *testing.T) {
	databaseURL := os.Getenv("OPTIMIZER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPTIMIZER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT code FROM ref.city WHERE catalog_revision > 0`)
	if err != nil {
		t.Fatal(err)
	}
	cities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(cities) == 0 {
		t.Fatalf("cities %v: %v", cities, err)
	}
	catalog := postgres.NewCatalog(poolDB{pool})
	for _, city := range cities {
		t.Run(city, func(t *testing.T) {
			want, err := catalog.LoadSlice(ctx, city, time.Now().Add(-24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			data, err := encode(want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decode(data)
			if err != nil {
				t.Fatal(err)
			}
			for i := range want.Places {
				if !reflect.DeepEqual(got.Places[i], want.Places[i]) {
					t.Fatalf("place %d changed:\n got %+v\nwant %+v", i, got.Places[i], want.Places[i])
				}
			}
			for i := range want.Sessions {
				if !reflect.DeepEqual(got.Sessions[i], want.Sessions[i]) {
					t.Fatalf("session %d changed:\n got %+v\nwant %+v", i, got.Sessions[i], want.Sessions[i])
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("slice header changed")
			}
			t.Logf("%d places, %d sessions, %d bytes", len(want.Places), len(want.Sessions), len(data))
		})
	}
}

package db

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/andres1m/impuls-goroda/pkg/config"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPoolCollectorReportsStats(t *testing.T) {
	// No connection is opened until the first acquire, so the pool needs no server.
	pool, err := pgxpool.New(context.Background(), "postgres://gateway_svc@127.0.0.1:1/impuls?pool_max_conns=4")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	collector := newPoolCollector("gateway_svc", pool.Stat)

	if n := testutil.CollectAndCount(collector); n != 7 {
		t.Fatalf("metrics = %d", n)
	}
	want := `
# HELP db_pool_max_conns Maximum size of the connection pool.
# TYPE db_pool_max_conns gauge
db_pool_max_conns{role="gateway_svc"} 4
`
	if err := testutil.CollectAndCompare(collector, strings.NewReader(want), "db_pool_max_conns"); err != nil {
		t.Fatal(err)
	}
}

func registeredRoles(t *testing.T) []string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, f := range families {
		if f.GetName() != "db_pool_max_conns" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				roles = append(roles, l.GetValue())
			}
		}
	}
	return roles
}

func TestInitRegistersPoolMetricsUntilStop(t *testing.T) {
	c, err := NewDB(nil, config.Database{Host: "postgres://syncer_svc@127.0.0.1:1/impuls"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if roles := registeredRoles(t); !slices.Contains(roles, "syncer_svc") {
		t.Fatalf("registered roles after init: %v", roles)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if roles := registeredRoles(t); slices.Contains(roles, "syncer_svc") {
		t.Fatal("pool metrics outlive the pool")
	}
}

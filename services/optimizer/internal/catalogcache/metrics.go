package catalogcache

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	buildBucketStart         = 0.005
	buildBucketFactor        = 2
	buildBucketCount         = 12
	invalidationBucketStart  = 0.05
	invalidationBucketFactor = 2
	invalidationBucketCount  = 10
)

var (
	sliceHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "optimizer_catalog_slice_hits_total",
		Help: "Requests served by where their catalog slice came from: trusted memory, memory after a revision check, the shared cache or the database.",
	}, []string{"level"})
	revisionChecks = promauto.NewCounter(prometheus.CounterOpts{
		Name: "optimizer_catalog_revision_checks_total",
		Help: "Requests that read the city's catalog revision from the database because the memory copy was not trusted.",
	})
	buildSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "optimizer_catalog_slice_build_seconds",
		Help:    "Time to read a city's catalog slice from the database.",
		Buckets: prometheus.ExponentialBuckets(buildBucketStart, buildBucketFactor, buildBucketCount),
	})
	sliceRevision = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "optimizer_catalog_slice_revision",
		Help: "Catalog revision of the slice each city has in memory.",
	}, []string{"city"})
	subscriptionHealthy = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "optimizer_catalog_subscription_healthy",
		Help: "1 while catalog announcements reach the optimizer and memory slices are trusted without a revision check.",
	})
	reconciles = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "optimizer_catalog_reconcile_total",
		Help: "Revision comparisons with the database by result: match, behind (a slice was older), gone (the city left the catalog) or error.",
	}, []string{"result"})
	invalidationLag = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "optimizer_catalog_invalidation_lag_seconds",
		Help: "Time from a catalog publication to its announcement reaching the optimizer.",
		Buckets: prometheus.ExponentialBuckets(
			invalidationBucketStart,
			invalidationBucketFactor,
			invalidationBucketCount,
		),
	})
	invalidationErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "optimizer_catalog_invalidation_errors_total",
		Help: "Catalog announcements that could not be read.",
	})
	l2Errors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "optimizer_catalog_l2_errors_total",
		Help: "Failed reads and writes of catalog slices in the shared cache.",
	}, []string{"op"})
)

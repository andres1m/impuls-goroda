package db

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolCollector reads the pool's counters at scrape time. The role label tells pools apart
// when a process connects under more than one database user.
type poolCollector struct {
	stat func() *pgxpool.Stat

	acquired, idle, total, max               *prometheus.Desc
	acquires, emptyAcquires, acquireDuration *prometheus.Desc
}

func newPoolCollector(role string, stat func() *pgxpool.Stat) *poolCollector {
	labels := prometheus.Labels{"role": role}
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("db_pool_"+name, help, nil, labels)
	}
	return &poolCollector{
		stat:            stat,
		acquired:        desc("acquired_conns", "Connections currently in use."),
		idle:            desc("idle_conns", "Idle connections in the pool."),
		total:           desc("total_conns", "Open connections in the pool."),
		max:             desc("max_conns", "Maximum size of the connection pool."),
		acquires:        desc("acquires_total", "Successful connection acquires."),
		emptyAcquires:   desc("empty_acquires_total", "Acquires that waited because the pool had no idle connection."),
		acquireDuration: desc("acquire_duration_seconds_total", "Total time spent acquiring connections."),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.acquires, c.emptyAcquires, c.acquireDuration} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stat()
	ch <- prometheus.MustNewConstMetric(c.acquired, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(c.total, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.emptyAcquires, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, s.AcquireDuration().Seconds())
}

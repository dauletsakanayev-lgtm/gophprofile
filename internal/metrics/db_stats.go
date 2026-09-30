package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
)

// RegisterDBStats регистрирует gauge-функции, которые на скрейпе читают
// sql.DB.Stats() — открытые/idle/in-use соединения, счётчики ожидания.
func RegisterDBStats(db *sql.DB) {
	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_connections_open",
			Help: "Number of established connections both in use and idle.",
		},
		func() float64 { return float64(db.Stats().OpenConnections) },
	))
	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_connections_in_use",
			Help: "Number of connections currently in use.",
		},
		func() float64 { return float64(db.Stats().InUse) },
	))
	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_connections_idle",
			Help: "Number of idle connections.",
		},
		func() float64 { return float64(db.Stats().Idle) },
	))
	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_wait_count_total",
			Help: "Total number of connections waited for.",
		},
		func() float64 { return float64(db.Stats().WaitCount) },
	))
	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "db_wait_seconds_total",
			Help: "Total time blocked waiting for a new connection.",
		},
		func() float64 { return db.Stats().WaitDuration.Seconds() },
	))
}

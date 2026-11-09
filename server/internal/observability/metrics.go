// Package observability exposes Prometheus metrics for batch migrations.
// Metrics are served on a separate listener (METRICS_ADDR) so the public API
// port never exposes them.
package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	batchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "migration_batch_total",
		Help: "Jumlah batch migration berdasarkan status akhir.",
	}, []string{"status"})

	itemsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "migration_batch_items_total",
		Help: "Jumlah item batch migration berdasarkan status.",
	}, []string{"status"})

	itemDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "migration_item_duration_seconds",
		Help:    "Durasi eksekusi satu item migration di dalam batch.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"status"})

	batchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "migration_batch_duration_seconds",
		Help:    "Durasi total batch migration dari mulai sampai selesai.",
		Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	})
)

// ObserveItem records the terminal status and duration of one batch item.
func ObserveItem(status string, durationMs int64) {
	itemsTotal.WithLabelValues(status).Inc()
	itemDuration.WithLabelValues(status).Observe(float64(durationMs) / 1000)
}

// ObserveBatch records the terminal status and wall-clock duration of a batch.
func ObserveBatch(status string, duration time.Duration) {
	batchTotal.WithLabelValues(status).Inc()
	if duration > 0 {
		batchDuration.Observe(duration.Seconds())
	}
}

// Handler returns the Prometheus scrape handler.
func Handler() http.Handler { return promhttp.Handler() }

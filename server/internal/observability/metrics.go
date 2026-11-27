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

	jobsByStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "migration_jobs",
		Help: "Jumlah job migration saat ini berdasarkan status.",
	}, []string{"status"})

	queueWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "migration_queue_wait_seconds",
		Help:    "Waktu tunggu job di queue sebelum diambil worker.",
		Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
	})

	jobDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "migration_duration_seconds",
		Help:    "Durasi eksekusi satu attempt migration dari mulai sampai selesai.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
	})

	retriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "migration_retries_total",
		Help: "Jumlah attempt migration yang merupakan retry (attempt > 1).",
	})

	lockBusyTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "migration_lock_busy_total",
		Help: "Jumlah job yang dikembalikan ke queue karena lock database sedang dipakai.",
	})

	poolConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "db_pool_connections",
		Help: "Jumlah koneksi database pool berdasarkan state (in_use atau idle).",
	}, []string{"state"})

	poolMax = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "db_pool_max_connections",
		Help: "Batas maksimum koneksi seluruh pool database aplikasi.",
	})

	poolWaitSeconds = promauto.NewCounter(prometheus.CounterOpts{
		Name: "db_pool_wait_seconds_total",
		Help: "Total waktu menunggu koneksi dari pool database aplikasi.",
	})
)

// jobStatuses dipakai agar status yang tidak muncul di query tetap 0 (bukan hilang).
var jobStatuses = []string{"queued", "running", "success", "partial", "failed", "uncertain", "cancelled"}

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

// SetJobCounts mengganti nilai gauge migration_jobs untuk semua status.
func SetJobCounts(counts map[string]int64) {
	for _, status := range jobStatuses {
		jobsByStatus.WithLabelValues(status).Set(float64(counts[status]))
	}
}

// ObserveQueueWait mencatat waktu tunggu job di queue.
func ObserveQueueWait(wait time.Duration) {
	if wait < 0 {
		wait = 0
	}
	queueWait.Observe(wait.Seconds())
}

// ObserveJobDuration mencatat durasi satu attempt migration.
func ObserveJobDuration(durationMs int64) {
	if durationMs < 0 {
		return
	}
	jobDuration.Observe(float64(durationMs) / 1000)
}

// IncRetry menghitung attempt yang merupakan retry.
func IncRetry() { retriesTotal.Inc() }

// IncLockBusy menghitung job yang dikembalikan ke queue karena lock dipakai.
func IncLockBusy() { lockBusyTotal.Inc() }

// SetPoolStats mengganti gauge koneksi pool dan menambah counter waktu tunggu.
// waitDeltaSeconds adalah selisih kumulatif sejak pemanggilan sebelumnya.
func SetPoolStats(inUse, idle, max int, waitDeltaSeconds float64) {
	poolConnections.WithLabelValues("in_use").Set(float64(inUse))
	poolConnections.WithLabelValues("idle").Set(float64(idle))
	poolMax.Set(float64(max))
	if waitDeltaSeconds > 0 {
		poolWaitSeconds.Add(waitDeltaSeconds)
	}
}

// Handler returns the Prometheus scrape handler.
func Handler() http.Handler { return promhttp.Handler() }

// Package metrics — определения Prometheus-метрик GophProfile.
// Все счётчики/гистограммы/gauges создаются через promauto → авто-регистрация
// в default registry. Экспорт: /metrics endpoint.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ---------- HTTP (RED) ----------

// HTTPRequestsTotal — количество HTTP-запросов по методу, route и статусу.
var HTTPRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests count by method, route, status.",
	},
	[]string{"method", "route", "status"},
)

// HTTPRequestDuration — гистограмма длительностей HTTP-запросов.
var HTTPRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration by method and route.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"method", "route"},
)

// ---------- Business (avatars) ----------

// AvatarsUploadsTotal — попытки загрузки по итоговому статусу.
// status: success | bad_mime | too_large | error.
var AvatarsUploadsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "avatars_uploads_total",
		Help: "Total avatar upload attempts by outcome status.",
	},
	[]string{"status"},
)

// AvatarsUploadDuration — длительность полной операции загрузки
// (валидация + S3 put + DB insert + publish).
var AvatarsUploadDuration = promauto.NewHistogram(
	prometheus.HistogramOpts{
		Name:    "avatars_upload_duration_seconds",
		Help:    "Full avatar upload duration.",
		Buckets: prometheus.DefBuckets,
	},
)

// ---------- Worker ----------

// WorkerJobsTotal — обработанные задачи по очереди и статусу.
// status: success | failed | skipped.
var WorkerJobsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "worker_jobs_processed_total",
		Help: "Worker jobs processed by queue and status.",
	},
	[]string{"queue", "status"},
)

// WorkerJobDuration — длительность обработки одной задачи по очереди.
var WorkerJobDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "worker_job_duration_seconds",
		Help:    "Worker job duration by queue.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"queue"},
)

// AvatarsStorageBytes — суммарный размер активных аватаров в S3 (обновляется
// периодическим poll'ом из БД).
var AvatarsStorageBytes = promauto.NewGauge(
	prometheus.GaugeOpts{
		Name: "avatars_storage_bytes",
		Help: "Total bytes stored (sum of active avatars' size_bytes).",
	},
)

// AMQPQueueDepth — глубина очереди RabbitMQ (ready messages).
var AMQPQueueDepth = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "amqp_queue_depth",
		Help: "RabbitMQ queue depth (ready messages).",
	},
	[]string{"queue"},
)

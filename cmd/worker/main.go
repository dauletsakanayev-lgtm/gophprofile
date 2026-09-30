// Command gophprofile-worker — потребитель задач RabbitMQ.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/logging"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/tracing"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/worker"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultDSN         = "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"
	defaultS3Endpoint  = "localhost:9000"
	defaultS3AccessKey = "minioadmin"
	defaultS3SecretKey = "minioadmin"
	defaultS3Bucket    = "avatars"
	defaultAMQP        = "amqp://guest:guest@localhost:5673/"
	defaultLogLevel    = "info"
	defaultOTLP        = "localhost:4317"
)

func main() {
	logger := logging.New(envOr("LOG_LEVEL", defaultLogLevel))
	slog.SetDefault(logger)

	// OpenTelemetry
	ctxInit := context.Background()
	shutdown, err := tracing.Init(ctxInit, "gophprofile-worker",
		envOr("OTEL_EXPORTER_OTLP_ENDPOINT", defaultOTLP))
	if err != nil {
		slog.Error("tracing init", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(shutCtx)
	}()
	slog.Info("tracing initialized")

	slog.Info("gophprofile-worker starting")

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	// Prometheus /metrics — отдельный HTTP на :8081
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		srv := &http.Server{
			Addr:              ":8081",
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		slog.Info("worker /metrics listening", slog.String("addr", ":8081"))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("worker metrics server", slog.String("err", err.Error()))
		}
	}()

	db, err := storage.Open(envOr("DB_DSN", defaultDSN))
	if err != nil {
		slog.Error("open db", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	s3, err := storage.NewS3Store(ctx, storage.S3Config{
		Endpoint:  envOr("S3_ENDPOINT", defaultS3Endpoint),
		AccessKey: envOr("S3_ACCESS_KEY", defaultS3AccessKey),
		SecretKey: envOr("S3_SECRET_KEY", defaultS3SecretKey),
		Bucket:    envOr("S3_BUCKET", defaultS3Bucket),
		UseSSL:    false,
	})
	if err != nil {
		slog.Error("s3 init", slog.String("err", err.Error()))
		os.Exit(1)
	}

	conn, ch, err := broker.Connect(envOr("AMQP_URL", defaultAMQP))
	if err != nil {
		slog.Error("amqp connect", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer conn.Close()
	defer ch.Close()

	delCh, err := conn.Channel()
	if err != nil {
		slog.Error("open delete channel", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer delCh.Close()

	repo := storage.NewPostgresAvatarRepo(db)
	proc := worker.NewProcessor(repo, s3)
	delProc := worker.NewDeleteProcessor(s3)

	slog.Info("worker consuming",
		slog.String("upload_queue", broker.QueueName),
		slog.String("delete_queue", broker.DeleteQueueName))

	errCh := make(chan error, 2)
	go func() { errCh <- broker.Consume(ctx, ch, proc.Handle) }()
	go func() { errCh <- broker.ConsumeDelete(ctx, delCh, delProc.Handle) }()

	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("consume", slog.String("err", err.Error()))
		os.Exit(1)
	}
	slog.Info("worker shutdown")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

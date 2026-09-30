// Command gophprofile-server поднимает HTTP-сервер GophProfile.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	httpsrv "github.com/dauletsakanayev-lgtm/gophprofile/internal/http"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/logging"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/metrics"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/service"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/tracing"
)

const (
	defaultDSN         = "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"
	defaultS3Endpoint  = "localhost:9000"
	defaultS3AccessKey = "minioadmin"
	defaultS3SecretKey = "minioadmin"
	defaultS3Bucket    = "avatars"
	defaultAMQP        = "amqp://guest:guest@localhost:5673/"
	defaultHTTPAddr    = ":8080"
	defaultLogLevel    = "info"
	defaultOTLP        = "localhost:4317"
)

func main() {
	logger := logging.New(envOr("LOG_LEVEL", defaultLogLevel))
	slog.SetDefault(logger)

	// OpenTelemetry
	ctxInit := context.Background()
	shutdown, err := tracing.Init(ctxInit, "gophprofile-server",
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

	dsn := envOr("DB_DSN", defaultDSN)

	slog.Info("gophprofile-server starting")
	db, err := storage.Open(dsn)
	if err != nil {
		slog.Error("open db", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("applying migrations")
	if err := storage.Migrate(db); err != nil {
		slog.Error("migrate", slog.String("err", err.Error()))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	slog.Info("connecting to S3/MinIO")
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
	slog.Info("S3 bucket ready", slog.String("bucket", defaultS3Bucket))

	slog.Info("connecting to RabbitMQ")
	amqpConn, amqpCh, err := broker.Connect(envOr("AMQP_URL", defaultAMQP))
	if err != nil {
		slog.Error("amqp connect", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer amqpConn.Close()
	defer amqpCh.Close()
	slog.Info("RabbitMQ queue ready", slog.String("queue", broker.QueueName))

	pub := broker.NewPublisher(amqpCh)

	// Метрики: DB connections + storage bytes + queue depth
	metrics.RegisterDBStats(db)
	metrics.StartStorageCollector(ctx, db, 30*time.Second)

	// Отдельный AMQP канал для queue-depth collector'а, чтобы не мешать publisher'у
	depthCh, err := amqpConn.Channel()
	if err != nil {
		slog.Error("open depth channel", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer depthCh.Close()
	broker.StartQueueDepthCollector(ctx, depthCh, 15*time.Second)

	repo := storage.NewPostgresAvatarRepo(db)
	svc := service.New(repo, s3, pub)
	ah := httpsrv.NewAvatarHandler(svc)
	hh := httpsrv.NewHealthHandler(db, s3, amqpCh)
	srv := httpsrv.New(envOr("HTTP_ADDR", defaultHTTPAddr), ah, hh, logger)

	if err := srv.Run(ctx); err != nil {
		slog.Error("http server", slog.String("err", err.Error()))
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

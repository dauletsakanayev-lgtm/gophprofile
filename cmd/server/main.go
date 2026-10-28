// Command gophprofile-server поднимает HTTP-сервер GophProfile.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	httpsrv "github.com/dauletsakanayev-lgtm/gophprofile/internal/http"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/logging"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/metrics"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/resilience"
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
	if err := run(); err != nil {
		slog.Error("fatal", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	logger := logging.New(envOr("LOG_LEVEL", defaultLogLevel))
	slog.SetDefault(logger)

	// OpenTelemetry
	ctxInit := context.Background()
	shutdown, err := tracing.Init(ctxInit, "gophprofile-server",
		envOr("OTEL_EXPORTER_OTLP_ENDPOINT", defaultOTLP))
	if err != nil {
		return fmt.Errorf("tracing init: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(shutCtx)
	}()
	slog.Info("tracing initialized")

	slog.Info("gophprofile-server starting")
	db, err := storage.Open(envOr("DB_DSN", defaultDSN))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	slog.Info("applying migrations")
	if err := storage.Migrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// MIGRATE_ONLY=true → этот процесс используется Helm pre-install/pre-upgrade
	// hook'ом только для прогона миграций; HTTP-сервер не стартуем, defer'ы
	// (shutdown trace exporter, close db) отрабатывают, exit 0.
	if envOr("MIGRATE_ONLY", "") == "true" {
		slog.Info("migrate-only mode: migrations applied, exiting")
		return nil
	}

	// Переопределение бизнес-правил из ConfigMap: MAX_FILE_SIZE (байты)
	// и ALLOWED_MIME_TYPES (CSV). Пустые значения — остаются дефолты из service.
	var maxFileSize int64
	if v := envOr("MAX_FILE_SIZE", ""); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxFileSize = n
		} else {
			slog.Warn("ignoring invalid MAX_FILE_SIZE",
				slog.String("value", v),
				slog.String("err", "parse int or non-positive"))
		}
	}
	var mimeTypes []string
	if v := envOr("ALLOWED_MIME_TYPES", ""); v != "" {
		mimeTypes = strings.Split(v, ",")
	}
	if maxFileSize > 0 || len(mimeTypes) > 0 {
		service.Configure(maxFileSize, mimeTypes)
		slog.Info("service configured from env",
			slog.Int64("max_file_size", service.MaxUploadBytes))
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
		return fmt.Errorf("s3 init: %w", err)
	}
	slog.Info("S3 bucket ready", slog.String("bucket", envOr("S3_BUCKET", defaultS3Bucket)))

	slog.Info("connecting to RabbitMQ")
	amqpConn, amqpCh, err := broker.Connect(envOr("AMQP_URL", defaultAMQP))
	if err != nil {
		return fmt.Errorf("amqp connect: %w", err)
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
		return fmt.Errorf("open depth channel: %w", err)
	}
	defer depthCh.Close()
	broker.StartQueueDepthCollector(ctx, depthCh, 15*time.Second)

	// Внешние зависимости оборачиваем в circuit breaker'ы (защита от каскадного
	// отказа при деградации). При превышении лимита ошибок breaker переходит в
	// Open и возвращает ErrOpenState мгновенно, не трогая реальный ресурс.
	repo := resilience.NewAvatarRepo(storage.NewPostgresAvatarRepo(db))
	cbS3 := resilience.NewS3Store(s3)
	cbPub := resilience.NewPublisher(pub)

	svc := service.New(repo, cbS3, cbPub)
	ah := httpsrv.NewAvatarHandler(svc)
	// Для healthz используем сырой s3/amqpCh — проверяется реальная доступность,
	// не состояние breaker'а.
	hh := httpsrv.NewHealthHandler(db, s3, amqpCh)
	srv := httpsrv.New(envOr("HTTP_ADDR", defaultHTTPAddr), ah, hh, logger)

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("http server: %w", err)
	}
	slog.Info("shutdown complete")
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

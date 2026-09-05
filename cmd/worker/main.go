// Command gophprofile-worker — потребитель задач RabbitMQ.
// GET оригинала → ресайз → PUT processed → обновление статуса.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/worker"
)

const (
	defaultDSN         = "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"
	defaultS3Endpoint  = "localhost:9000"
	defaultS3AccessKey = "minioadmin"
	defaultS3SecretKey = "minioadmin"
	defaultS3Bucket    = "avatars"
	defaultAMQP        = "amqp://guest:guest@localhost:5673/"
)

func main() {
	log.Println("gophprofile-worker: starting...")

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	db, err := storage.Open(envOr("DB_DSN", defaultDSN))
	if err != nil {
		log.Fatalf("open db: %v", err)
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
		log.Fatalf("s3 init: %v", err)
	}

	conn, ch, err := broker.Connect(envOr("AMQP_URL", defaultAMQP))
	if err != nil {
		log.Fatalf("amqp connect: %v", err)
	}
	defer conn.Close()
	defer ch.Close()

	repo := storage.NewPostgresAvatarRepo(db)
	proc := worker.NewProcessor(repo, s3)

	log.Println("gophprofile-worker: consuming", broker.QueueName)
	if err := broker.Consume(ctx, ch, proc.Handle); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("consume: %v", err)
	}
	log.Println("gophprofile-worker: shutdown")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

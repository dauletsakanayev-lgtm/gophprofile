// Command gophprofile-server поднимает HTTP-сервер GophProfile.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	httpsrv "github.com/dauletsakanayev-lgtm/gophprofile/internal/http"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
)

const (
	defaultDSN         = "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"
	defaultS3Endpoint  = "localhost:9000"
	defaultS3AccessKey = "minioadmin"
	defaultS3SecretKey = "minioadmin"
	defaultS3Bucket    = "avatars"
	defaultAMQP        = "amqp://guest:guest@localhost:5673/"
	defaultHTTPAddr    = ":8080"
)

func main() {
	dsn := envOr("DB_DSN", defaultDSN)

	log.Println("gophprofile-server: connecting to postgres...")
	db, err := storage.Open(dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	log.Println("applying migrations...")
	if err := storage.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied successfully")

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	log.Println("connecting to S3/MinIO...")
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
	log.Println("S3 bucket ready:", defaultS3Bucket)

	log.Println("connecting to RabbitMQ...")
	amqpConn, amqpCh, err := broker.Connect(envOr("AMQP_URL", defaultAMQP))
	if err != nil {
		log.Fatalf("amqp connect: %v", err)
	}
	defer amqpConn.Close()
	defer amqpCh.Close()
	log.Println("RabbitMQ queue ready:", broker.QueueName)

	pub := broker.NewPublisher(amqpCh)
	repo := storage.NewPostgresAvatarRepo(db)
	ah := httpsrv.NewAvatarHandler(repo, s3, pub)
	srv := httpsrv.New(envOr("HTTP_ADDR", defaultHTTPAddr), ah)

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("http server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

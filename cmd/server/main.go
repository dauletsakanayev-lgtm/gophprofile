// Command gophprofile-server поднимает HTTP-сервер GophProfile.
// Пока умеет применить миграции и инициализировать S3 —
// HTTP появится в подшаге D.
package main

import (
	"context"
	"log"
	"os"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
)

const (
	defaultDSN         = "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"
	defaultS3Endpoint  = "localhost:9000"
	defaultS3AccessKey = "minioadmin"
	defaultS3SecretKey = "minioadmin"
	defaultS3Bucket    = "avatars"
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

	log.Println("connecting to S3/MinIO...")
	ctx := context.Background()
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

	_ = s3 // будет использован HTTP-обработчиками в подшаге D
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Command gophprofile-server поднимает HTTP-сервер GophProfile.
// Пока умеет только применить миграции — HTTP появится в следующем подшаге.
package main

import (
	"log"
	"os"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
)

const defaultDSN = "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"

func main() {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}

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
}

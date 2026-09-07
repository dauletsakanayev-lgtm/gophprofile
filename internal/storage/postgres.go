package storage

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/db"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open открывает пул соединений к PostgreSQL и проверяет доступность.
func Open(dsn string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	if err := pool.Ping(); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Migrate применяет все *.up.sql миграции по порядку имён.
// Отслеживает применённые версии в таблице schema_migrations.
func Migrate(pool *sql.DB) error {
	if _, err := pool.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(db.MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var up []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".up.sql") {
			up = append(up, name)
		}
	}
	sort.Strings(up)

	for _, name := range up {
		version := strings.TrimSuffix(name, ".up.sql")

		var count int
		if err := pool.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = $1`, version,
		).Scan(&count); err != nil {
			return fmt.Errorf("check %s: %w", version, err)
		}
		if count > 0 {
			continue
		}

		body, err := fs.ReadFile(db.MigrationsFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := pool.Exec(string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := pool.Exec(
			`INSERT INTO schema_migrations(version) VALUES ($1)`, version,
		); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
	}
	return nil
}

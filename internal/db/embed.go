// Package db предоставляет ресурсы БД (миграции), встроенные в бинарь.
package db

import "embed"

// MigrationsFS содержит все SQL-миграции в подпапке migrations/.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

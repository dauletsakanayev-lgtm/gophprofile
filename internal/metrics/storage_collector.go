package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// StartStorageCollector запускает горутину, которая раз в interval
// пересчитывает суммарный объём хранилища из БД и обновляет gauge
// avatars_storage_bytes. Останавливается при отмене ctx.
func StartStorageCollector(ctx context.Context, db *sql.DB, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()

		update := func() {
			var total sql.NullInt64
			err := db.QueryRowContext(ctx,
				`SELECT COALESCE(SUM(size_bytes), 0) FROM avatars WHERE deleted_at IS NULL`,
			).Scan(&total)
			if err != nil {
				slog.WarnContext(ctx, "storage collector query failed",
					slog.String("err", err.Error()))
				return
			}
			AvatarsStorageBytes.Set(float64(total.Int64))
		}

		update() // сразу при старте
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				update()
			}
		}
	}()
}

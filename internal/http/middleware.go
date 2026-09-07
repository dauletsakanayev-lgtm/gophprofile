// Package http — HTTP-транспорт GophProfile (chi router + хендлеры).
package http

import (
	"context"
	"net/http"
)

type ctxKey int

const userIDKey ctxKey = 1

// AuthMiddleware извлекает user_id из заголовка X-User-ID.
// user_id — произвольная строка длиной 1-255 (email, uuid, никнейм).
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-User-ID")
		if id == "" || len(id) > 255 {
			http.Error(w, "X-User-ID header required (1-255 chars)", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

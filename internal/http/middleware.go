// Package http — HTTP-транспорт GophProfile (chi router + хендлеры).
package http

import (
	"context"
	"net/http"
	"strconv"
)

type ctxKey int

const userIDKey ctxKey = 1

// AuthMiddleware извлекает user_id из заголовка X-User-ID.
// Временное решение — на следующем этапе заменим на JWT.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("X-User-ID")
		if h == "" {
			http.Error(w, "X-User-ID header required", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(h, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "X-User-ID must be positive integer", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userIDFromCtx(ctx context.Context) int64 {
	v, _ := ctx.Value(userIDKey).(int64)
	return v
}

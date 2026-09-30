package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// HTTPMetrics — chi middleware: считает http_requests_total и
// http_request_duration_seconds по методу, chi route pattern и статусу.
func HTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		defer func() {
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unknown"
			}
			status := strconv.Itoa(ww.Status())
			metrics.HTTPRequestsTotal.WithLabelValues(r.Method, route, status).Inc()
			metrics.HTTPRequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		}()
		next.ServeHTTP(ww, r)
	})
}

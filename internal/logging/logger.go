// Package logging — обёртка над log/slog: JSON-формат + автоматическое
// поле trace_id из OpenTelemetry-span (заполняется в подшаге B).
package logging

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// New создаёт JSON-логгер с заданным уровнем.
// level: "debug" | "info" | "warn" | "error"; неизвестное значение → info.
func New(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(&traceHandler{Handler: base})
}

// traceHandler добавляет поле "trace_id" в каждую запись, когда в контексте
// есть активный OTel-span.
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if tid := traceIDFromCtx(ctx); tid != "" {
		r.AddAttrs(slog.String("trace_id", tid))
	}
	return h.Handler.Handle(ctx, r)
}

// traceIDFromCtx — заглушка. В подшаге B будет реализовано через
//
//	sc := trace.SpanContextFromContext(ctx)
//	if sc.HasTraceID() { return sc.TraceID().String() }
func traceIDFromCtx(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

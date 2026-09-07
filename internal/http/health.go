package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	amqp "github.com/rabbitmq/amqp091-go"
)

// HealthHandler — GET /health, проверяет БД, S3, брокер.
type HealthHandler struct {
	db     *sql.DB
	s3     *storage.S3Store
	amqpCh *amqp.Channel
}

func NewHealthHandler(db *sql.DB, s3 *storage.S3Store, amqpCh *amqp.Channel) *HealthHandler {
	return &HealthHandler{db: db, s3: s3, amqpCh: amqpCh}
}

// ServeHTTP реализует http.Handler.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	comps := map[string]string{
		"database": pingDB(ctx, h.db),
		"s3":       pingS3(ctx, h.s3),
		"broker":   pingBroker(h.amqpCh),
	}

	overall := "ok"
	for _, s := range comps {
		if s != "ok" {
			overall = "degraded"
			break
		}
	}

	status := http.StatusOK
	if overall != "ok" {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     overall,
		"components": comps,
	})
}

func pingDB(ctx context.Context, db *sql.DB) string {
	if err := db.PingContext(ctx); err != nil {
		return "down: " + err.Error()
	}
	return "ok"
}

func pingS3(ctx context.Context, s3 *storage.S3Store) string {
	if err := s3.HealthCheck(ctx); err != nil {
		return "down: " + err.Error()
	}
	return "ok"
}

// pingBroker пассивно проверяет что очередь существует и канал жив.
func pingBroker(ch *amqp.Channel) string {
	if ch == nil || ch.IsClosed() {
		return "down: channel closed"
	}
	if _, err := ch.QueueDeclarePassive(broker.QueueName, true, false, false, false, nil); err != nil {
		return "down: " + err.Error()
	}
	return "ok"
}

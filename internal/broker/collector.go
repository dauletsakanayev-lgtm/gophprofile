package broker

import (
	"context"
	"log/slog"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/metrics"
	amqp "github.com/rabbitmq/amqp091-go"
)

// StartQueueDepthCollector раз в interval опрашивает длины наших очередей
// через AMQP QueueInspect (passive queue.declare) и обновляет метрики
// amqp_queue_depth{queue}. Использует отдельный канал ch, чтобы не мешать
// consumer/publisher каналам.
func StartQueueDepthCollector(ctx context.Context, ch *amqp.Channel, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()

		queues := []string{QueueName, DeleteQueueName}
		update := func() {
			for _, q := range queues {
				info, err := ch.QueueInspect(q)
				if err != nil {
					slog.WarnContext(ctx, "queue inspect failed",
						slog.String("queue", q),
						slog.String("err", err.Error()))
					continue
				}
				metrics.AMQPQueueDepth.WithLabelValues(q).Set(float64(info.Messages))
			}
		}

		update()
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

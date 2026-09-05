package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Handler — функция обработки одной задачи.
type Handler func(ctx context.Context, task AvatarTask) error

// Consume подписывается на очередь и передаёт каждое сообщение в handler.
// Prefetch=1 — по одному сообщению за раз, ack только после успеха.
// Возвращает при отмене ctx или закрытии канала.
func Consume(ctx context.Context, ch *amqp.Channel, handler Handler) error {
	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}
	msgs, err := ch.Consume(QueueName, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				return errors.New("consumer channel closed")
			}
			processOne(ctx, msg, handler)
		}
	}
}

func processOne(ctx context.Context, msg amqp.Delivery, handler Handler) {
	var task AvatarTask
	if err := json.Unmarshal(msg.Body, &task); err != nil {
		log.Printf("broker: bad message body: %v", err)
		_ = msg.Nack(false, false) // без requeue — payload битый
		return
	}
	if err := handler(ctx, task); err != nil {
		log.Printf("broker: handler failed for %s: %v", task.AvatarID, err)
		_ = msg.Nack(false, false) // без requeue — статус уже failed в БД
		return
	}
	_ = msg.Ack(false)
}

// Package broker — обёртка над RabbitMQ (amqp091) для GophProfile.
package broker

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// QueueName — имя очереди для задач обработки аватаров.
const QueueName = "avatars.new"

// AvatarTask — payload сообщения в очереди.
type AvatarTask struct {
	AvatarID    string `json:"avatar_id"`
	OriginalKey string `json:"original_key"`
}

// Connect подключается к RabbitMQ по URL и объявляет durable очередь.
// Возвращает соединение и канал; вызывающий обязан закрыть оба.
func Connect(url string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, nil, fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("channel: %w", err)
	}
	if _, err := ch.QueueDeclare(QueueName, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, fmt.Errorf("declare queue %s: %w", QueueName, err)
	}
	return conn, ch, nil
}

// Publisher публикует задачи в очередь avatars.new.
type Publisher struct {
	ch *amqp.Channel
}

func NewPublisher(ch *amqp.Channel) *Publisher {
	return &Publisher{ch: ch}
}

// Publish отправляет задачу как persistent JSON-сообщение в default exchange
// с routing key = имя очереди.
func (p *Publisher) Publish(ctx context.Context, task AvatarTask) error {
	body, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	return p.ch.PublishWithContext(ctx,
		"",        // default exchange
		QueueName, // routing key
		false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
}

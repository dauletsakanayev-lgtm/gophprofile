// Package broker — обёртка над RabbitMQ (amqp091) для GophProfile.
package broker

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TaskPublisher — контракт публикации задач (для мокирования в тестах).
type TaskPublisher interface {
	Publish(ctx context.Context, task AvatarTask) error
	PublishDelete(ctx context.Context, task DeleteTask) error
}

// QueueName — имя очереди для задач обработки аватаров.
const QueueName = "avatars.new"

// AvatarTask — payload сообщения в очереди.
type AvatarTask struct {
	AvatarID    string `json:"avatar_id"`
	OriginalKey string `json:"original_key"`
}

// DeleteQueueName — очередь задач на удаление файлов из S3.
const DeleteQueueName = "avatars.delete"

// DeleteTask — payload задачи удаления: список S3-ключей к очистке.
type DeleteTask struct {
	AvatarID string   `json:"avatar_id"`
	S3Keys   []string `json:"s3_keys"`
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
	if _, err := ch.QueueDeclare(DeleteQueueName, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, fmt.Errorf("declare queue %s: %w", DeleteQueueName, err)
	}
	return conn, ch, nil
}

// Publisher публикует задачи в очередь avatars.new.
type Publisher struct {
	ch *amqp.Channel
}

var brokerTracer = otel.Tracer("gophprofile/broker")

// injectHeaders кодирует trace-context в AMQP-headers.
func injectHeaders(ctx context.Context) amqp.Table {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	headers := amqp.Table{}
	for k, v := range carrier {
		headers[k] = v
	}
	return headers
}

func NewPublisher(ch *amqp.Channel) *Publisher {
	return &Publisher{ch: ch}
}

// Publish отправляет задачу как persistent JSON-сообщение в default exchange
// с routing key = имя очереди.
func (p *Publisher) Publish(ctx context.Context, task AvatarTask) error {
	ctx, span := brokerTracer.Start(ctx, "amqp.publish "+QueueName,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination", QueueName),
			attribute.String("avatar_id", task.AvatarID),
		))
	defer span.End()

	body, err := json.Marshal(task)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("marshal task: %w", err)
	}

	err = p.ch.PublishWithContext(ctx,
		"", QueueName, false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
			Headers:      injectHeaders(ctx),
		},
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// PublishDelete отправляет задачу удаления файлов из S3 в очередь avatars.delete.
func (p *Publisher) PublishDelete(ctx context.Context, task DeleteTask) error {
	ctx, span := brokerTracer.Start(ctx, "amqp.publish "+DeleteQueueName,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination", DeleteQueueName),
			attribute.String("avatar_id", task.AvatarID),
		))
	defer span.End()

	body, err := json.Marshal(task)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("marshal delete task: %w", err)
	}

	err = p.ch.PublishWithContext(ctx,
		"", DeleteQueueName, false, false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
			Headers:      injectHeaders(ctx),
		},
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

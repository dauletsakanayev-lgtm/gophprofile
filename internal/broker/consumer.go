package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Handler — функция обработки одной задачи.
type Handler func(ctx context.Context, task AvatarTask) error

// extractCtx извлекает trace-context из AMQP-headers.
func extractCtx(base context.Context, headers amqp.Table) context.Context {
	carrier := propagation.MapCarrier{}
	for k, v := range headers {
		if s, ok := v.(string); ok {
			carrier[k] = s
		}
	}
	return otel.GetTextMapPropagator().Extract(base, carrier)
}

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
	ctx = extractCtx(ctx, msg.Headers)
	ctx, span := brokerTracer.Start(ctx, "amqp.consume "+QueueName,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.source", QueueName),
		))
	defer span.End()

	var task AvatarTask
	if err := json.Unmarshal(msg.Body, &task); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "broker: bad message body",
			slog.String("err", err.Error()))
		_ = msg.Nack(false, false)
		return
	}
	span.SetAttributes(attribute.String("avatar_id", task.AvatarID))

	if err := handler(ctx, task); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "broker: handler failed",
			slog.String("avatar_id", task.AvatarID),
			slog.String("err", err.Error()))
		_ = msg.Nack(false, false)
		return
	}
	_ = msg.Ack(false)
}

// DeleteHandler — обработчик задачи удаления.
type DeleteHandler func(ctx context.Context, task DeleteTask) error

// ConsumeDelete подписывается на очередь avatars.delete.
func ConsumeDelete(ctx context.Context, ch *amqp.Channel, handler DeleteHandler) error {
	msgs, err := ch.Consume(DeleteQueueName, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume delete: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				return errors.New("delete consumer channel closed")
			}
			processDeleteOne(ctx, msg, handler)
		}
	}
}

func processDeleteOne(ctx context.Context, msg amqp.Delivery, handler DeleteHandler) {
	ctx = extractCtx(ctx, msg.Headers)
	ctx, span := brokerTracer.Start(ctx, "amqp.consume "+DeleteQueueName,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.source", DeleteQueueName),
		))
	defer span.End()

	var task DeleteTask
	if err := json.Unmarshal(msg.Body, &task); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "broker: bad delete message",
			slog.String("err", err.Error()))
		_ = msg.Nack(false, false)
		return
	}
	span.SetAttributes(attribute.String("avatar_id", task.AvatarID))

	if err := handler(ctx, task); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "broker: delete handler failed",
			slog.String("avatar_id", task.AvatarID),
			slog.String("err", err.Error()))
		_ = msg.Nack(false, false)
		return
	}
	_ = msg.Ack(false)
}

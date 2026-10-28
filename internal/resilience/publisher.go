package resilience

import (
	"context"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/sony/gobreaker"
)

// Publisher оборачивает TaskPublisher с circuit breaker.
type Publisher struct {
	inner broker.TaskPublisher
	cb    *gobreaker.CircuitBreaker
}

// NewPublisher создаёт обёртку с breaker'ом name="rabbitmq".
func NewPublisher(inner broker.TaskPublisher) *Publisher {
	return &Publisher{inner: inner, cb: New("rabbitmq")}
}

func (p *Publisher) Publish(ctx context.Context, task broker.AvatarTask) error {
	return Do(p.cb, func() error {
		return p.inner.Publish(ctx, task)
	})
}

func (p *Publisher) PublishDelete(ctx context.Context, task broker.DeleteTask) error {
	return Do(p.cb, func() error {
		return p.inner.PublishDelete(ctx, task)
	})
}

// Package resilience — обёртки с circuit breaker'ом вокруг внешних зависимостей
// (S3/MinIO, RabbitMQ, Postgres). При деградации зависимости breaker переходит
// в состояние Open и возвращает ошибки мгновенно без обращения к ресурсу —
// защита от каскадного отказа.
package resilience

import (
	"log/slog"
	"time"

	"github.com/sony/gobreaker"
)

// New создаёт circuit breaker с базовыми настройками + логирование смены состояния.
//
// Поведение:
//   - 5 consecutive failures ИЛИ failure rate > 60% на окне ≥ 10 запросов
//     → переход Closed → Open.
//   - В Open состоянии все запросы фейлятся мгновенно с gobreaker.ErrOpenState.
//   - Через 10 секунд → Half-Open: пропускается до 3 пробных запросов.
//   - 3 успешных подряд → возврат в Closed. Любая ошибка → снова Open.
func New(name string) *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 3,
		Timeout:     10 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			if c.ConsecutiveFailures >= 5 {
				return true
			}
			if c.Requests >= 10 {
				failureRate := float64(c.TotalFailures) / float64(c.Requests)
				return failureRate > 0.6
			}
			return false
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			slog.Warn("circuit breaker state change",
				slog.String("name", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()))
		},
	})
}

// Do выполняет fn под защитой breaker'а. Возвращает ошибку из fn
// или gobreaker.ErrOpenState если breaker открыт.
func Do(cb *gobreaker.CircuitBreaker, fn func() error) error {
	_, err := cb.Execute(func() (interface{}, error) {
		return nil, fn()
	})
	return err
}

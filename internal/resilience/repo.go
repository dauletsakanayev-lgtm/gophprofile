package resilience

import (
	"context"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/google/uuid"
	"github.com/sony/gobreaker"
)

// AvatarRepo оборачивает AvatarRepository с circuit breaker.
type AvatarRepo struct {
	inner storage.AvatarRepository
	cb    *gobreaker.CircuitBreaker
}

// NewAvatarRepo создаёт обёртку с breaker'ом name="postgres".
func NewAvatarRepo(inner storage.AvatarRepository) *AvatarRepo {
	return &AvatarRepo{inner: inner, cb: New("postgres")}
}

func (r *AvatarRepo) Create(ctx context.Context, a *model.Avatar) (*model.Avatar, error) {
	var out *model.Avatar
	err := Do(r.cb, func() error {
		var e error
		out, e = r.inner.Create(ctx, a)
		return e
	})
	return out, err
}

func (r *AvatarRepo) Get(ctx context.Context, id uuid.UUID) (*model.Avatar, error) {
	var out *model.Avatar
	err := Do(r.cb, func() error {
		var e error
		out, e = r.inner.Get(ctx, id)
		return e
	})
	return out, err
}

func (r *AvatarRepo) GetByUser(ctx context.Context, userID string) (*model.Avatar, error) {
	var out *model.Avatar
	err := Do(r.cb, func() error {
		var e error
		out, e = r.inner.GetByUser(ctx, userID)
		return e
	})
	return out, err
}

func (r *AvatarRepo) ListByUser(ctx context.Context, userID string) ([]*model.Avatar, error) {
	var out []*model.Avatar
	err := Do(r.cb, func() error {
		var e error
		out, e = r.inner.ListByUser(ctx, userID)
		return e
	})
	return out, err
}

func (r *AvatarRepo) SetProcessing(ctx context.Context, id uuid.UUID) error {
	return Do(r.cb, func() error {
		return r.inner.SetProcessing(ctx, id)
	})
}

func (r *AvatarRepo) SetCompleted(ctx context.Context, id uuid.UUID, thumbnails map[string]string, width, height int) error {
	return Do(r.cb, func() error {
		return r.inner.SetCompleted(ctx, id, thumbnails, width, height)
	})
}

func (r *AvatarRepo) SetFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	return Do(r.cb, func() error {
		return r.inner.SetFailed(ctx, id, errMsg)
	})
}

func (r *AvatarRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	return Do(r.cb, func() error {
		return r.inner.SoftDelete(ctx, id)
	})
}

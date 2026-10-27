package resilience

import (
	"context"
	"io"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/sony/gobreaker"
)

// S3Store оборачивает ObjectStore с circuit breaker.
type S3Store struct {
	inner storage.ObjectStore
	cb    *gobreaker.CircuitBreaker
}

// NewS3Store создаёт обёртку с breaker'ом name="s3".
func NewS3Store(inner storage.ObjectStore) *S3Store {
	return &S3Store{inner: inner, cb: New("s3")}
}

func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	return Do(s.cb, func() error {
		return s.inner.Put(ctx, key, r, size, contentType)
	})
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	var out io.ReadCloser
	err := Do(s.cb, func() error {
		var e error
		out, e = s.inner.Get(ctx, key)
		return e
	})
	return out, err
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	return Do(s.cb, func() error {
		return s.inner.Delete(ctx, key)
	})
}

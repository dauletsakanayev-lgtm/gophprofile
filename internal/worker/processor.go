// Package worker — обработчик задач аватаров: две миниатюры + идемпотентность.
package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/disintegration/imaging"
	"github.com/google/uuid"
)

// Размеры миниатюр по ТЗ.
var thumbSizes = []struct {
	label string
	w, h  int
}{
	{"100x100", 100, 100},
	{"300x300", 300, 300},
}

const (
	jpegQuality = 85
	maxAttempts = 3
	baseBackoff = 200 * time.Millisecond
)

// Processor реализует broker.Handler.
type Processor struct {
	repo storage.AvatarRepository
	s3   storage.ObjectStore
}

func NewProcessor(repo storage.AvatarRepository, s3 storage.ObjectStore) *Processor {
	return &Processor{repo: repo, s3: s3}
}

// Handle: pending -> processing -> completed|failed, с идемпотентностью и retry.
func (p *Processor) Handle(ctx context.Context, task broker.AvatarTask) error {
	id, err := uuid.Parse(task.AvatarID)
	if err != nil {
		return fmt.Errorf("bad avatar id: %w", err)
	}

	// Идемпотентность: SetProcessing переводит только из 'pending'.
	// Если сейчас processing/completed — пропускаем (не ошибка).
	if err := p.repo.SetProcessing(ctx, id); err != nil {
		if errors.Is(err, storage.ErrAvatarNotFound) {
			log.Printf("worker: %s skipped (already processed or deleted)", id)
			return nil
		}
		return fmt.Errorf("set processing: %w", err)
	}

	thumbs, width, height, procErr := p.processWithRetry(ctx, id, task.OriginalKey)
	if procErr != nil {
		msg := procErr.Error()
		_ = p.repo.SetFailed(ctx, id, msg)
		log.Printf("worker: %s failed: %v", id, procErr)
		return procErr
	}

	if err := p.repo.SetCompleted(ctx, id, thumbs, width, height); err != nil {
		return fmt.Errorf("set completed: %w", err)
	}
	return nil
}

// processWithRetry делает до maxAttempts попыток обработки с экспоненциальной задержкой.
func (p *Processor) processWithRetry(ctx context.Context, id uuid.UUID, originalKey string) (map[string]string, int, int, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		thumbs, w, h, err := p.processOne(ctx, id, originalKey)
		if err == nil {
			return thumbs, w, h, nil
		}
		lastErr = err
		log.Printf("worker: %s attempt %d/%d failed: %v", id, attempt, maxAttempts, err)
		if attempt < maxAttempts {
			backoff := baseBackoff << (attempt - 1)
			select {
			case <-ctx.Done():
				return nil, 0, 0, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return nil, 0, 0, fmt.Errorf("all %d attempts failed: %w", maxAttempts, lastErr)
}

// processOne скачивает оригинал, генерирует обе миниатюры и загружает их в S3.
func (p *Processor) processOne(ctx context.Context, id uuid.UUID, originalKey string) (map[string]string, int, int, error) {
	obj, err := p.s3.Get(ctx, originalKey)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("get original: %w", err)
	}
	defer obj.Close()

	raw, err := io.ReadAll(obj)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("read original: %w", err)
	}

	img, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	out := make(map[string]string, len(thumbSizes))
	for _, ts := range thumbSizes {
		thumb := imaging.Fit(img, ts.w, ts.h, imaging.Lanczos)
		var buf bytes.Buffer
		if err := imaging.Encode(&buf, thumb, imaging.JPEG, imaging.JPEGQuality(jpegQuality)); err != nil {
			return nil, 0, 0, fmt.Errorf("encode %s: %w", ts.label, err)
		}
		key := fmt.Sprintf("thumbnails/%s/%s.jpg", id, ts.label)
		if err := p.s3.Put(ctx, key, &buf, int64(buf.Len()), "image/jpeg"); err != nil {
			return nil, 0, 0, fmt.Errorf("put %s: %w", ts.label, err)
		}
		out[ts.label] = key
	}
	return out, width, height, nil
}

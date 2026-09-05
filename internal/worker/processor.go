// Package worker — обработчик задач аватаров: ресайз и загрузка обработанной версии.
package worker

import (
	"bytes"
	"context"
	"fmt"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/disintegration/imaging"
	"github.com/google/uuid"
)

const (
	thumbW  = 256
	thumbH  = 256
	quality = 85
)

// Processor реализует broker.Handler.
type Processor struct {
	repo storage.AvatarRepository
	s3   *storage.S3Store
}

func NewProcessor(repo storage.AvatarRepository, s3 *storage.S3Store) *Processor {
	return &Processor{repo: repo, s3: s3}
}

// Handle делает полный цикл: pending → processing → (ready|failed).
// Возвращает ошибку — Consumer сделает Nack.
func (p *Processor) Handle(ctx context.Context, task broker.AvatarTask) error {
	id, err := uuid.Parse(task.AvatarID)
	if err != nil {
		return fmt.Errorf("bad avatar id: %w", err)
	}

	if err := p.repo.UpdateStatus(ctx, id, model.AvatarProcessing, nil, nil); err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}

	processedKey, procErr := p.processOne(ctx, id, task.OriginalKey)
	if procErr != nil {
		msg := procErr.Error()
		_ = p.repo.UpdateStatus(ctx, id, model.AvatarFailed, nil, &msg)
		return procErr
	}

	if err := p.repo.UpdateStatus(ctx, id, model.AvatarReady, &processedKey, nil); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	return nil
}

func (p *Processor) processOne(ctx context.Context, id uuid.UUID, originalKey string) (string, error) {
	obj, err := p.s3.Get(ctx, originalKey)
	if err != nil {
		return "", fmt.Errorf("get original: %w", err)
	}
	defer obj.Close()

	img, err := imaging.Decode(obj, imaging.AutoOrientation(true))
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}

	thumb := imaging.Fit(img, thumbW, thumbH, imaging.Lanczos)

	var buf bytes.Buffer
	if err := imaging.Encode(&buf, thumb, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
		return "", fmt.Errorf("encode image: %w", err)
	}

	key := "processed/" + id.String()
	if err := p.s3.Put(ctx, key, &buf, int64(buf.Len()), "image/jpeg"); err != nil {
		return "", fmt.Errorf("put processed: %w", err)
	}
	return key, nil
}

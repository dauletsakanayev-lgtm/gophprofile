package worker

import (
	"context"
	"log"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
)

// DeleteProcessor удаляет файлы из S3 по задаче из очереди avatars.delete.
type DeleteProcessor struct {
	s3 storage.ObjectStore
}

func NewDeleteProcessor(s3 storage.ObjectStore) *DeleteProcessor {
	return &DeleteProcessor{s3: s3}
}

// Handle — best-effort удаление всех ключей задачи.
// Ошибка на одном ключе не мешает удалению остальных.
func (p *DeleteProcessor) Handle(ctx context.Context, task broker.DeleteTask) error {
	for _, key := range task.S3Keys {
		if err := p.s3.Delete(ctx, key); err != nil {
			log.Printf("delete-worker: %s key=%s: %v", task.AvatarID, key, err)
		}
	}
	return nil
}

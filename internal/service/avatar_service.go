// Package service — бизнес-логика GophProfile.
// Handler'ы (транспорт) не работают с БД/S3/брокером напрямую — только через этот слой.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/google/uuid"
)

// Бизнес-ошибки. Транспорт мапит их в HTTP-статусы.
var (
	ErrForbidden         = errors.New("forbidden")
	ErrUnsupportedMIME   = errors.New("unsupported mime type")
	ErrFileTooLarge      = errors.New("file too large")
	ErrInvalidSize       = errors.New("invalid size")
	ErrThumbnailNotReady = errors.New("thumbnail not ready")
	ErrThumbnailNotFound = errors.New("thumbnail not found")
)

// MaxUploadBytes — лимит размера загружаемого файла (10 MB по ТЗ).
const MaxUploadBytes int64 = 10 << 20

// allowedMIME — разрешённые типы файлов.
var allowedMIME = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

// UploadCmd — параметры операции загрузки.
type UploadCmd struct {
	UserID    string
	FileName  string
	MimeType  string
	SizeBytes int64
	Reader    io.Reader
}

// AvatarService — контракт бизнес-логики.
type AvatarService interface {
	Upload(ctx context.Context, cmd UploadCmd) (*model.Avatar, error)
	Get(ctx context.Context, id uuid.UUID) (*model.Avatar, error)
	GetByUser(ctx context.Context, userID string) (*model.Avatar, error)
	ListByUser(ctx context.Context, userID string) ([]*model.Avatar, error)
	OpenFile(ctx context.Context, a *model.Avatar, size string) (io.ReadCloser, string, error)
	Delete(ctx context.Context, id uuid.UUID, requesterUserID string) error
	DeleteByUser(ctx context.Context, userID, requesterUserID string) error
}

// Service реализует AvatarService поверх repo + s3 + broker.
type Service struct {
	repo storage.AvatarRepository
	s3   storage.ObjectStore
	pub  broker.TaskPublisher
}

// New создаёт сервис.
func New(repo storage.AvatarRepository, s3 storage.ObjectStore, pub broker.TaskPublisher) *Service {
	return &Service{repo: repo, s3: s3, pub: pub}
}

// Upload валидирует MIME/размер, кладёт в S3 → сохраняет в БД → публикует в брокер.
// При ошибке БД компенсирует загрузку в S3 (удаляет осиротевший объект).
func (s *Service) Upload(ctx context.Context, cmd UploadCmd) (*model.Avatar, error) {
	if _, ok := allowedMIME[cmd.MimeType]; !ok {
		return nil, ErrUnsupportedMIME
	}
	if cmd.SizeBytes > MaxUploadBytes {
		return nil, ErrFileTooLarge
	}

	id := uuid.New()
	key := "original/" + id.String()

	if err := s.s3.Put(ctx, key, cmd.Reader, cmd.SizeBytes, cmd.MimeType); err != nil {
		return nil, fmt.Errorf("s3 put: %w", err)
	}

	created, err := s.repo.Create(ctx, &model.Avatar{
		ID: id, UserID: cmd.UserID, FileName: cmd.FileName,
		MimeType: cmd.MimeType, SizeBytes: cmd.SizeBytes, S3Key: key,
	})
	if err != nil {
		_ = s.s3.Delete(ctx, key) // компенсация
		return nil, fmt.Errorf("repo create: %w", err)
	}

	// Публикация задачи для воркера — best-effort:
	// status в БД остаётся 'pending', ошибку публикации логируем.
	if err := s.pub.Publish(ctx, broker.AvatarTask{
		AvatarID: created.ID.String(), OriginalKey: created.S3Key,
	}); err != nil {
		log.Printf("publish task %s: %v", created.ID, err)
	}
	return created, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*model.Avatar, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) GetByUser(ctx context.Context, userID string) (*model.Avatar, error) {
	return s.repo.GetByUser(ctx, userID)
}

func (s *Service) ListByUser(ctx context.Context, userID string) ([]*model.Avatar, error) {
	return s.repo.ListByUser(ctx, userID)
}

// OpenFile отдаёт reader из S3 для запрошенного размера + правильный Content-Type.
// size="" трактуется как "original".
func (s *Service) OpenFile(ctx context.Context, a *model.Avatar, size string) (io.ReadCloser, string, error) {
	if size == "" {
		size = "original"
	}
	var key, ct string
	switch size {
	case "original":
		key, ct = a.S3Key, a.MimeType
	case "100x100", "300x300":
		if a.ProcessingStatus != model.ProcessingCompleted {
			return nil, "", ErrThumbnailNotReady
		}
		k, ok := a.ThumbnailKeys[size]
		if !ok {
			return nil, "", ErrThumbnailNotFound
		}
		key, ct = k, "image/jpeg"
	default:
		return nil, "", ErrInvalidSize
	}

	obj, err := s.s3.Get(ctx, key)
	if err != nil {
		return nil, "", err
	}
	return obj, ct, nil
}

// Delete: авторизация владельца + soft delete в БД + best-effort очистка S3.
func (s *Service) Delete(ctx context.Context, id uuid.UUID, requesterUserID string) error {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if a.UserID != requesterUserID {
		return ErrForbidden
	}
	if err := s.repo.SoftDelete(ctx, id); err != nil {
		return err
	}
	s.cleanupS3(ctx, a)
	return nil
}

// DeleteByUser удаляет последний active-аватар пользователя.
func (s *Service) DeleteByUser(ctx context.Context, userID, requesterUserID string) error {
	if userID != requesterUserID {
		return ErrForbidden
	}
	a, err := s.repo.GetByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.repo.SoftDelete(ctx, a.ID); err != nil {
		return err
	}
	s.cleanupS3(ctx, a)
	return nil
}

// cleanupS3 отправляет задачу удаления в очередь (best-effort — если брокер недоступен,
// файлы можно почистить отдельным cleanup-джобом).
func (s *Service) cleanupS3(ctx context.Context, a *model.Avatar) {
	keys := []string{a.S3Key}
	for _, k := range a.ThumbnailKeys {
		keys = append(keys, k)
	}
	if err := s.pub.PublishDelete(ctx, broker.DeleteTask{
		AvatarID: a.ID.String(),
		S3Keys:   keys,
	}); err != nil {
		log.Printf("publish delete task %s: %v", a.ID, err)
	}
}

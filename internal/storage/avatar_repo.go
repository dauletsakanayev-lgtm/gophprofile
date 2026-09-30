package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/google/uuid"
)

// StaleProcessingTTL — сколько ждать перед повторной обработкой зависшей задачи
// (worker упал между SetProcessing и SetCompleted). После истечения TTL
// redelivery снова захватывает лизу.
const StaleProcessingTTL = 5 * time.Minute

// ErrAvatarNotFound — аватар не найден или soft-deleted или чужой.
var ErrAvatarNotFound = errors.New("avatar not found")

// AvatarRepository — контракт хранилища метаданных аватаров.
type AvatarRepository interface {
	Create(ctx context.Context, a *model.Avatar) (*model.Avatar, error)
	Get(ctx context.Context, id uuid.UUID) (*model.Avatar, error)
	GetByUser(ctx context.Context, userID string) (*model.Avatar, error) // последний active
	ListByUser(ctx context.Context, userID string) ([]*model.Avatar, error)
	SetProcessing(ctx context.Context, id uuid.UUID) error
	SetCompleted(ctx context.Context, id uuid.UUID, thumbnails map[string]string, width, height int) error
	SetFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
}

type PostgresAvatarRepo struct {
	db *sql.DB
}

func NewPostgresAvatarRepo(db *sql.DB) *PostgresAvatarRepo {
	return &PostgresAvatarRepo{db: db}
}

// scanAvatar заполняет *model.Avatar из sql-строки.
type scanner interface{ Scan(dest ...any) error }

func scanAvatar(a *model.Avatar, sc scanner) error {
	var thumbsRaw []byte
	if err := sc.Scan(&a.ID, &a.UserID, &a.FileName, &a.MimeType, &a.SizeBytes,
		&a.S3Key, &thumbsRaw, &a.UploadStatus, &a.ProcessingStatus,
		&a.Width, &a.Height,
		&a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
		return err
	}
	if len(thumbsRaw) > 0 {
		if err := json.Unmarshal(thumbsRaw, &a.ThumbnailKeys); err != nil {
			return fmt.Errorf("unmarshal thumbnails: %w", err)
		}
	}
	return nil
}

const avatarCols = `id, user_id, file_name, mime_type, size_bytes, s3_key,
                   thumbnail_s3_keys, upload_status, processing_status,
                   width_px, height_px,
                   created_at, updated_at, deleted_at`

// Create вставляет запись со статусом uploaded/pending (файл уже в S3).
func (r *PostgresAvatarRepo) Create(ctx context.Context, a *model.Avatar) (*model.Avatar, error) {
	const q = `
		INSERT INTO avatars (id, user_id, file_name, mime_type, size_bytes,
		                    s3_key, upload_status, processing_status)
		VALUES ($1, $2, $3, $4, $5, $6, 'uploaded', 'pending')
		RETURNING ` + avatarCols
	out := &model.Avatar{}
	if err := scanAvatar(out, r.db.QueryRowContext(ctx, q,
		a.ID, a.UserID, a.FileName, a.MimeType, a.SizeBytes, a.S3Key)); err != nil {
		return nil, fmt.Errorf("insert avatar: %w", err)
	}
	return out, nil
}

// Get возвращает аватар по id (только active — не soft-deleted).
func (r *PostgresAvatarRepo) Get(ctx context.Context, id uuid.UUID) (*model.Avatar, error) {
	const q = `SELECT ` + avatarCols + ` FROM avatars WHERE id = $1 AND deleted_at IS NULL`
	a := &model.Avatar{}
	err := scanAvatar(a, r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAvatarNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select avatar: %w", err)
	}
	return a, nil
}

// GetByUser возвращает последний active-аватар пользователя.
func (r *PostgresAvatarRepo) GetByUser(ctx context.Context, userID string) (*model.Avatar, error) {
	const q = `SELECT ` + avatarCols + `
	           FROM avatars
	           WHERE user_id = $1 AND deleted_at IS NULL
	           ORDER BY created_at DESC LIMIT 1`
	a := &model.Avatar{}
	err := scanAvatar(a, r.db.QueryRowContext(ctx, q, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAvatarNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select avatar by user: %w", err)
	}
	return a, nil
}

// ListByUser возвращает все active-аватары пользователя.
func (r *PostgresAvatarRepo) ListByUser(ctx context.Context, userID string) ([]*model.Avatar, error) {
	const q = `SELECT ` + avatarCols + `
	           FROM avatars
	           WHERE user_id = $1 AND deleted_at IS NULL
	           ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("query avatars: %w", err)
	}
	defer rows.Close()

	var out []*model.Avatar
	for rows.Next() {
		a := &model.Avatar{}
		if err := scanAvatar(a, rows); err != nil {
			return nil, fmt.Errorf("scan avatar: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetProcessing переводит processing_status в 'processing' (для идемпотентности —
// только если сейчас 'pending').
func (r *PostgresAvatarRepo) SetProcessing(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE avatars
		SET processing_status = 'processing', updated_at = NOW()
		WHERE id = $1
		  AND deleted_at IS NULL
		  AND (
		    processing_status = 'pending'
		    OR (processing_status = 'processing' AND updated_at < NOW() - $2::interval)
		  )`
	res, err := r.db.ExecContext(ctx, q, id, StaleProcessingTTL.String())
	if err != nil {
		return fmt.Errorf("set processing: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// либо completed/failed — терминальный статус,
		// либо processing со свежим updated_at — другой воркер обрабатывает
		return ErrAvatarNotFound
	}
	return nil
}

// SetCompleted проставляет 'completed' и сохраняет карту миниатюр.
func (r *PostgresAvatarRepo) SetCompleted(ctx context.Context, id uuid.UUID,
	thumbnails map[string]string, width, height int) error {
	body, err := json.Marshal(thumbnails)
	if err != nil {
		return fmt.Errorf("marshal thumbnails: %w", err)
	}
	const q = `
		UPDATE avatars
		SET processing_status = 'completed',
		    thumbnail_s3_keys = $1::jsonb,
		    width_px  = $2,
		    height_px = $3,
		    updated_at = NOW()
		WHERE id = $4 AND deleted_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, body, width, height, id)
	if err != nil {
		return fmt.Errorf("set completed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAvatarNotFound
	}
	return nil
}

// SetFailed проставляет 'failed' (текст ошибки логируется, в БД не храним).
func (r *PostgresAvatarRepo) SetFailed(ctx context.Context, id uuid.UUID, _ string) error {
	const q = `
		UPDATE avatars
		SET processing_status = 'failed', updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("set failed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAvatarNotFound
	}
	return nil
}

// SoftDelete проставляет deleted_at (мягкое удаление).
// Реальное удаление файлов из S3 делает воркер по событию.
func (r *PostgresAvatarRepo) SoftDelete(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE avatars
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAvatarNotFound
	}
	return nil
}

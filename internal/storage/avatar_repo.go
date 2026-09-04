package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/google/uuid"
)

// ErrAvatarNotFound — аватар не найден или принадлежит другому пользователю.
var ErrAvatarNotFound = errors.New("avatar not found")

// AvatarRepository — контракт хранилища метаданных аватаров.
type AvatarRepository interface {
	Create(ctx context.Context, a *model.Avatar) (*model.Avatar, error)
	Get(ctx context.Context, userID int64, id uuid.UUID) (*model.Avatar, error)
	ListByUser(ctx context.Context, userID int64) ([]*model.Avatar, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status model.AvatarStatus, processedKey, errMsg *string) error
	Delete(ctx context.Context, userID int64, id uuid.UUID) error
}

type PostgresAvatarRepo struct {
	db *sql.DB
}

func NewPostgresAvatarRepo(db *sql.DB) *PostgresAvatarRepo {
	return &PostgresAvatarRepo{db: db}
}

func scanAvatar(a *model.Avatar, sc interface{ Scan(...any) error }) error {
	return sc.Scan(&a.ID, &a.UserID, &a.Status, &a.OriginalKey,
		&a.ProcessedKey, &a.ContentType, &a.SizeBytes, &a.Error,
		&a.CreatedAt, &a.UpdatedAt)
}

func (r *PostgresAvatarRepo) Create(ctx context.Context, a *model.Avatar) (*model.Avatar, error) {
	const q = `
		INSERT INTO avatars (user_id, original_key, content_type, size_bytes)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, status, original_key, processed_key,
		          content_type, size_bytes, error, created_at, updated_at
	`
	out := &model.Avatar{}
	if err := scanAvatar(out, r.db.QueryRowContext(ctx, q,
		a.UserID, a.OriginalKey, a.ContentType, a.SizeBytes)); err != nil {
		return nil, fmt.Errorf("insert avatar: %w", err)
	}
	return out, nil
}

func (r *PostgresAvatarRepo) Get(ctx context.Context, userID int64, id uuid.UUID) (*model.Avatar, error) {
	const q = `
		SELECT id, user_id, status, original_key, processed_key,
		       content_type, size_bytes, error, created_at, updated_at
		FROM avatars WHERE id = $1 AND user_id = $2
	`
	a := &model.Avatar{}
	err := scanAvatar(a, r.db.QueryRowContext(ctx, q, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAvatarNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select avatar: %w", err)
	}
	return a, nil
}

func (r *PostgresAvatarRepo) ListByUser(ctx context.Context, userID int64) ([]*model.Avatar, error) {
	const q = `
		SELECT id, user_id, status, original_key, processed_key,
		       content_type, size_bytes, error, created_at, updated_at
		FROM avatars WHERE user_id = $1
		ORDER BY created_at DESC
	`
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

// UpdateStatus вызывается воркером после (успешной или неуспешной) обработки.
// processedKey и errMsg — опциональные (передавай nil где не нужно).
func (r *PostgresAvatarRepo) UpdateStatus(ctx context.Context,
	id uuid.UUID, status model.AvatarStatus,
	processedKey, errMsg *string) error {

	const q = `
		UPDATE avatars
		SET status = $1, processed_key = $2, error = $3, updated_at = NOW()
		WHERE id = $4
	`
	res, err := r.db.ExecContext(ctx, q, status, processedKey, errMsg, id)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAvatarNotFound
	}
	return nil
}

func (r *PostgresAvatarRepo) Delete(ctx context.Context, userID int64, id uuid.UUID) error {
	const q = `DELETE FROM avatars WHERE id = $1 AND user_id = $2`
	res, err := r.db.ExecContext(ctx, q, id, userID)
	if err != nil {
		return fmt.Errorf("delete avatar: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrAvatarNotFound
	}
	return nil
}

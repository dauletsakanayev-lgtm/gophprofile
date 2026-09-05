package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newMockRepo(t *testing.T) (*PostgresAvatarRepo, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return NewPostgresAvatarRepo(db), mock
}

func TestAvatarRepo_Create_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "status", "original_key", "processed_key",
		"content_type", "size_bytes", "error", "created_at", "updated_at",
	}).AddRow(id, int64(1), "pending", "original/x", nil,
		"image/jpeg", int64(100), nil, now, now)

	mock.ExpectQuery(`INSERT INTO avatars`).
		WithArgs(id, int64(1), "original/x", "image/jpeg", int64(100)).
		WillReturnRows(rows)

	got, err := repo.Create(context.Background(), &model.Avatar{
		ID: id, UserID: 1, OriginalKey: "original/x",
		ContentType: "image/jpeg", SizeBytes: 100,
	})
	require.NoError(t, err)
	require.Equal(t, model.AvatarPending, got.Status)
	require.Equal(t, int64(100), got.SizeBytes)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_Get_NotFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()

	mock.ExpectQuery(`SELECT .* FROM avatars WHERE`).
		WithArgs(id, int64(1)).
		WillReturnError(sql.ErrNoRows)

	_, err := repo.Get(context.Background(), 1, id)
	require.ErrorIs(t, err, ErrAvatarNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_Get_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "status", "original_key", "processed_key",
		"content_type", "size_bytes", "error", "created_at", "updated_at",
	}).AddRow(id, int64(1), "ready", "original/x", "processed/x",
		"image/jpeg", int64(200), nil, now, now)

	mock.ExpectQuery(`SELECT .* FROM avatars WHERE`).
		WithArgs(id, int64(1)).
		WillReturnRows(rows)

	a, err := repo.Get(context.Background(), 1, id)
	require.NoError(t, err)
	require.Equal(t, model.AvatarReady, a.Status)
	require.NotNil(t, a.ProcessedKey)
	require.Equal(t, "processed/x", *a.ProcessedKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_UpdateStatus_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	pk := "processed/x"

	mock.ExpectExec(`UPDATE avatars`).
		WithArgs(model.AvatarReady, &pk, (*string)(nil), id).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateStatus(context.Background(), id, model.AvatarReady, &pk, nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_UpdateStatus_NotFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()

	mock.ExpectExec(`UPDATE avatars`).
		WithArgs(model.AvatarFailed, (*string)(nil), (*string)(nil), id).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.UpdateStatus(context.Background(), id, model.AvatarFailed, nil, nil)
	require.ErrorIs(t, err, ErrAvatarNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_Delete_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()

	mock.ExpectExec(`DELETE FROM avatars`).
		WithArgs(id, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.Delete(context.Background(), 1, id))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_Delete_NotFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()

	mock.ExpectExec(`DELETE FROM avatars`).
		WithArgs(id, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.Delete(context.Background(), 1, id)
	require.ErrorIs(t, err, ErrAvatarNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_ListByUser(t *testing.T) {
	repo, mock := newMockRepo(t)
	id1, id2 := uuid.New(), uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "status", "original_key", "processed_key",
		"content_type", "size_bytes", "error", "created_at", "updated_at",
	}).
		AddRow(id1, int64(1), "ready", "original/a", "processed/a",
			"image/jpeg", int64(100), nil, now, now).
		AddRow(id2, int64(1), "pending", "original/b", nil,
			"image/png", int64(200), nil, now, now)

	mock.ExpectQuery(`SELECT .* FROM avatars WHERE user_id`).
		WithArgs(int64(1)).
		WillReturnRows(rows)

	out, err := repo.ListByUser(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.Equal(t, model.AvatarReady, out[0].Status)
	require.Equal(t, model.AvatarPending, out[1].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_ListByUser_Empty(t *testing.T) {
	repo, mock := newMockRepo(t)

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "status", "original_key", "processed_key",
		"content_type", "size_bytes", "error", "created_at", "updated_at",
	})
	mock.ExpectQuery(`SELECT .* FROM avatars WHERE user_id`).
		WithArgs(int64(42)).
		WillReturnRows(rows)

	out, err := repo.ListByUser(context.Background(), 42)
	require.NoError(t, err)
	require.Empty(t, out)
	require.NoError(t, mock.ExpectationsWereMet())
}

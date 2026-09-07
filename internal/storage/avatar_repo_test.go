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

var avatarColumns = []string{
	"id", "user_id", "file_name", "mime_type", "size_bytes", "s3_key",
	"thumbnail_s3_keys", "upload_status", "processing_status",
	"width_px", "height_px",
	"created_at", "updated_at", "deleted_at",
}

func TestAvatarRepo_Create_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows(avatarColumns).AddRow(
		id, "alice", "p.jpg", "image/jpeg", int64(100), "original/x",
		nil, "uploaded", "pending", nil, nil, now, now, nil)

	mock.ExpectQuery(`INSERT INTO avatars`).
		WithArgs(id, "alice", "p.jpg", "image/jpeg", int64(100), "original/x").
		WillReturnRows(rows)

	got, err := repo.Create(context.Background(), &model.Avatar{
		ID: id, UserID: "alice", FileName: "p.jpg",
		MimeType: "image/jpeg", SizeBytes: 100, S3Key: "original/x",
	})
	require.NoError(t, err)
	require.Equal(t, model.UploadUploaded, got.UploadStatus)
	require.Equal(t, model.ProcessingPending, got.ProcessingStatus)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_Get_NotFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectQuery(`SELECT .* FROM avatars WHERE id`).
		WithArgs(id).WillReturnError(sql.ErrNoRows)

	_, err := repo.Get(context.Background(), id)
	require.ErrorIs(t, err, ErrAvatarNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_Get_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows(avatarColumns).AddRow(
		id, "alice", "p.jpg", "image/jpeg", int64(200), "original/x",
		[]byte(`{"100x100":"th/100.jpg"}`), "uploaded", "completed", nil, nil, now, now, nil)

	mock.ExpectQuery(`SELECT .* FROM avatars WHERE id`).
		WithArgs(id).WillReturnRows(rows)

	a, err := repo.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, model.ProcessingCompleted, a.ProcessingStatus)
	require.Equal(t, "th/100.jpg", a.ThumbnailKeys["100x100"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_GetByUser_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows(avatarColumns).AddRow(
		id, "bob", "b.jpg", "image/png", int64(300), "original/b",
		nil, "uploaded", "completed", nil, nil, now, now, nil)

	mock.ExpectQuery(`FROM avatars\s+WHERE user_id`).
		WithArgs("bob").WillReturnRows(rows)

	a, err := repo.GetByUser(context.Background(), "bob")
	require.NoError(t, err)
	require.Equal(t, "bob", a.UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_ListByUser(t *testing.T) {
	repo, mock := newMockRepo(t)
	id1, id2 := uuid.New(), uuid.New()
	now := time.Now()

	rows := sqlmock.NewRows(avatarColumns).
		AddRow(id1, "alice", "a1.jpg", "image/jpeg", int64(100), "k1",
			nil, "uploaded", "completed", nil, nil, now, now, nil).
		AddRow(id2, "alice", "a2.jpg", "image/png", int64(200), "k2",
			nil, "uploaded", "pending", nil, nil, now, now, nil)

	mock.ExpectQuery(`FROM avatars\s+WHERE user_id`).
		WithArgs("alice").WillReturnRows(rows)

	out, err := repo.ListByUser(context.Background(), "alice")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_SetProcessing_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE avatars\s+SET processing_status = 'processing'`).
		WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SetProcessing(context.Background(), id))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_SetProcessing_NoRows(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE avatars\s+SET processing_status = 'processing'`).
		WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.SetProcessing(context.Background(), id)
	require.ErrorIs(t, err, ErrAvatarNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_SetCompleted_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE avatars\s+SET processing_status = 'completed'`).
		WithArgs(sqlmock.AnyArg(), 200, 100, id).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SetCompleted(context.Background(), id,
		map[string]string{"100x100": "th/100.jpg"}, 200, 100))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_SetFailed_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE avatars\s+SET processing_status = 'failed'`).
		WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SetFailed(context.Background(), id, "boom"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_SoftDelete_OK(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE avatars\s+SET deleted_at`).
		WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SoftDelete(context.Background(), id))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAvatarRepo_SoftDelete_NotFound(t *testing.T) {
	repo, mock := newMockRepo(t)
	id := uuid.New()
	mock.ExpectExec(`UPDATE avatars\s+SET deleted_at`).
		WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.SoftDelete(context.Background(), id)
	require.ErrorIs(t, err, ErrAvatarNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

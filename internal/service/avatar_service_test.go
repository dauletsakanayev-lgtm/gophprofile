package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ---------- fakes ----------

type fakeRepo struct {
	mu    sync.Mutex
	items map[uuid.UUID]*model.Avatar

	createErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{items: map[uuid.UUID]*model.Avatar{}} }

func (f *fakeRepo) Create(_ context.Context, a *model.Avatar) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	a.UploadStatus = model.UploadUploaded
	a.ProcessingStatus = model.ProcessingPending
	f.items[a.ID] = a
	return a, nil
}
func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok || a.DeletedAt != nil {
		return nil, storage.ErrAvatarNotFound
	}
	return a, nil
}
func (f *fakeRepo) GetByUser(_ context.Context, userID string) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.items {
		if a.UserID == userID && a.DeletedAt == nil {
			return a, nil
		}
	}
	return nil, storage.ErrAvatarNotFound
}
func (f *fakeRepo) ListByUser(_ context.Context, userID string) ([]*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*model.Avatar
	for _, a := range f.items {
		if a.UserID == userID && a.DeletedAt == nil {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeRepo) SetProcessing(context.Context, uuid.UUID) error { return nil }
func (f *fakeRepo) SetCompleted(_ context.Context, id uuid.UUID, thumbnails map[string]string, _, _ int) error {
	return nil
}
func (f *fakeRepo) SetFailed(context.Context, uuid.UUID, string) error { return nil }
func (f *fakeRepo) SoftDelete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok || a.DeletedAt != nil {
		return storage.ErrAvatarNotFound
	}
	now := time.Now()
	a.DeletedAt = &now
	return nil
}

type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	putErr  error
	getErr  error
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]byte{}} }

func (s *fakeS3) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if s.putErr != nil {
		return s.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}
func (s *fakeS3) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *fakeS3) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

type fakePub struct {
	tasks       []broker.AvatarTask
	deleteTasks []broker.DeleteTask
	err         error
}

func (p *fakePub) Publish(_ context.Context, t broker.AvatarTask) error {
	if p.err != nil {
		return p.err
	}
	p.tasks = append(p.tasks, t)
	return nil
}

func (p *fakePub) PublishDelete(_ context.Context, t broker.DeleteTask) error {
	if p.err != nil {
		return p.err
	}
	p.deleteTasks = append(p.deleteTasks, t)
	return nil
}

// ---------- Upload ----------

func TestService_Upload_OK(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	a, err := svc.Upload(context.Background(), UploadCmd{
		UserID: "alice", FileName: "a.jpg", MimeType: "image/jpeg",
		SizeBytes: 100, Reader: bytes.NewReader([]byte("body")),
	})
	require.NoError(t, err)
	require.Equal(t, "alice", a.UserID)
	require.Equal(t, model.UploadUploaded, a.UploadStatus)
	require.Contains(t, a.S3Key, "original/")
}

func TestService_Upload_UnsupportedMIME(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	_, err := svc.Upload(context.Background(), UploadCmd{
		MimeType: "application/pdf", Reader: bytes.NewReader([]byte("x")),
	})
	require.ErrorIs(t, err, ErrUnsupportedMIME)
}

func TestService_Upload_TooLarge(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	_, err := svc.Upload(context.Background(), UploadCmd{
		MimeType: "image/jpeg", SizeBytes: MaxUploadBytes + 1,
		Reader: bytes.NewReader([]byte("x")),
	})
	require.ErrorIs(t, err, ErrFileTooLarge)
}

func TestService_Upload_S3PutFails_NoInsert(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	s3.putErr = errors.New("boom")
	svc := New(repo, s3, &fakePub{})

	_, err := svc.Upload(context.Background(), UploadCmd{
		UserID: "u", MimeType: "image/jpeg", SizeBytes: 10,
		Reader: bytes.NewReader([]byte("x")),
	})
	require.Error(t, err)
	require.Empty(t, repo.items)
}

func TestService_Upload_DBFails_S3Compensated(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	repo.createErr = errors.New("db down")
	svc := New(repo, s3, pub)

	_, err := svc.Upload(context.Background(), UploadCmd{
		UserID: "u", MimeType: "image/jpeg", SizeBytes: 10,
		Reader: bytes.NewReader([]byte("x")),
	})
	require.Error(t, err)
	require.Empty(t, s3.objects, "S3 object must be compensated on DB failure")
	require.Empty(t, pub.tasks)
}

// ---------- OpenFile ----------

func TestService_OpenFile_Original(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	svc := New(repo, s3, &fakePub{})
	a := &model.Avatar{S3Key: "k", MimeType: "image/jpeg"}
	s3.objects["k"] = []byte("data")

	obj, ct, err := svc.OpenFile(context.Background(), a, "original")
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", ct)
	b, _ := io.ReadAll(obj)
	obj.Close()
	require.Equal(t, "data", string(b))
}

func TestService_OpenFile_Thumbnail_NotReady(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	svc := New(repo, s3, &fakePub{})
	a := &model.Avatar{ProcessingStatus: model.ProcessingPending}

	_, _, err := svc.OpenFile(context.Background(), a, "100x100")
	require.ErrorIs(t, err, ErrThumbnailNotReady)
}

func TestService_OpenFile_Thumbnail_OK(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	svc := New(repo, s3, &fakePub{})
	a := &model.Avatar{
		ProcessingStatus: model.ProcessingCompleted,
		ThumbnailKeys:    map[string]string{"100x100": "th/100"},
	}
	s3.objects["th/100"] = []byte("thumb")

	obj, ct, err := svc.OpenFile(context.Background(), a, "100x100")
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", ct)
	b, _ := io.ReadAll(obj)
	obj.Close()
	require.Equal(t, "thumb", string(b))
}

func TestService_OpenFile_InvalidSize(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	_, _, err := svc.OpenFile(context.Background(), &model.Avatar{}, "bogus")
	require.ErrorIs(t, err, ErrInvalidSize)
}

// ---------- Delete ----------

func TestService_Delete_OK(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	pub := &fakePub{}
	svc := New(repo, s3, pub)
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: "alice", S3Key: "k"}
	s3.objects["k"] = []byte("d")

	require.NoError(t, svc.Delete(context.Background(), id, "alice"))
	require.NotNil(t, repo.items[id].DeletedAt)
	// Async cleanup: S3 остаётся, задача уходит в очередь — воркер удалит.
	require.NotEmpty(t, s3.objects)
	require.Len(t, pub.deleteTasks, 1)
	require.Equal(t, id.String(), pub.deleteTasks[0].AvatarID)
	require.Contains(t, pub.deleteTasks[0].S3Keys, "k")
}

func TestService_Delete_Forbidden(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	svc := New(repo, s3, &fakePub{})
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: "alice", S3Key: "k"}
	s3.objects["k"] = []byte("d")

	err := svc.Delete(context.Background(), id, "bob")
	require.ErrorIs(t, err, ErrForbidden)
	require.Nil(t, repo.items[id].DeletedAt)
	require.NotEmpty(t, s3.objects)
}

func TestService_Delete_NotFound(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	err := svc.Delete(context.Background(), uuid.New(), "alice")
	require.ErrorIs(t, err, storage.ErrAvatarNotFound)
}

func TestService_DeleteByUser_ForbiddenForOtherRequester(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	err := svc.DeleteByUser(context.Background(), "alice", "bob")
	require.ErrorIs(t, err, ErrForbidden)
}

// ---------- List / Get ----------

func TestService_ListByUser(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	svc := New(repo, s3, &fakePub{})
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: "alice"}

	out, err := svc.ListByUser(context.Background(), "alice")
	require.NoError(t, err)
	require.Len(t, out, 1)
}

func TestService_GetByUser_NotFound(t *testing.T) {
	svc := New(newFakeRepo(), newFakeS3(), &fakePub{})
	_, err := svc.GetByUser(context.Background(), "ghost")
	require.ErrorIs(t, err, storage.ErrAvatarNotFound)
}

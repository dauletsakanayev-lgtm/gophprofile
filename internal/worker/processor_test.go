package worker

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"sync"
	"testing"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	mu         sync.Mutex
	items      map[uuid.UUID]*model.Avatar
	processing []uuid.UUID
	completed  []uuid.UUID
	failed     []uuid.UUID
	setProcErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{items: map[uuid.UUID]*model.Avatar{}} }

func (f *fakeRepo) Create(context.Context, *model.Avatar) (*model.Avatar, error) { return nil, nil }
func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.items[id]; ok {
		return a, nil
	}
	return nil, storage.ErrAvatarNotFound
}
func (f *fakeRepo) GetByUser(context.Context, string) (*model.Avatar, error)    { return nil, nil }
func (f *fakeRepo) ListByUser(context.Context, string) ([]*model.Avatar, error) { return nil, nil }
func (f *fakeRepo) SoftDelete(context.Context, uuid.UUID) error                 { return nil }

func (f *fakeRepo) SetProcessing(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setProcErr != nil {
		return f.setProcErr
	}
	f.processing = append(f.processing, id)
	return nil
}

func (f *fakeRepo) SetCompleted(_ context.Context, id uuid.UUID, thumbnails map[string]string, _, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, id)
	if a, ok := f.items[id]; ok {
		a.ThumbnailKeys = thumbnails
	}
	return nil
}

func (f *fakeRepo) SetFailed(_ context.Context, id uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, id)
	return nil
}

type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	getErr  error
	putErr  error
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

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for x := 0; x < 400; x++ {
		for y := 0; y < 400; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func TestProcessor_Completed_TwoThumbnails(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: "u", S3Key: "original/x"}
	s3.objects["original/x"] = tinyJPEG(t)

	p := NewProcessor(repo, s3)
	require.NoError(t, p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	}))

	require.Equal(t, []uuid.UUID{id}, repo.processing)
	require.Equal(t, []uuid.UUID{id}, repo.completed)
	require.Empty(t, repo.failed)

	key100 := "thumbnails/" + id.String() + "/100x100.jpg"
	key300 := "thumbnails/" + id.String() + "/300x300.jpg"
	require.Contains(t, s3.objects, key100)
	require.Contains(t, s3.objects, key300)
}

func TestProcessor_IdempotentSkip(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	repo.setProcErr = storage.ErrAvatarNotFound
	id := uuid.New()

	p := NewProcessor(repo, s3)
	require.NoError(t, p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	}))
	require.Empty(t, repo.processing)
	require.Empty(t, repo.completed)
	require.Empty(t, repo.failed)
}

func TestProcessor_S3GetFails_MarkFailed(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	s3.getErr = errors.New("s3 down")
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: "u"}

	p := NewProcessor(repo, s3)
	require.Error(t, p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	}))
	require.Equal(t, []uuid.UUID{id}, repo.processing)
	require.Equal(t, []uuid.UUID{id}, repo.failed)
	require.Empty(t, repo.completed)
}

func TestProcessor_DecodeFails_MarkFailed(t *testing.T) {
	repo, s3 := newFakeRepo(), newFakeS3()
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: "u"}
	s3.objects["original/x"] = []byte("not-an-image")

	p := NewProcessor(repo, s3)
	require.Error(t, p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	}))
	require.Equal(t, []uuid.UUID{id}, repo.failed)
}

func TestProcessor_BadUUID(t *testing.T) {
	p := NewProcessor(newFakeRepo(), newFakeS3())
	require.Error(t, p.Handle(context.Background(), broker.AvatarTask{AvatarID: "bad"}))
}

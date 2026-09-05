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

// ---------- fakes ----------

type fakeRepo struct {
	mu       sync.Mutex
	statuses []model.AvatarStatus
	items    map[uuid.UUID]*model.Avatar
}

func newFakeRepo() *fakeRepo { return &fakeRepo{items: map[uuid.UUID]*model.Avatar{}} }

func (f *fakeRepo) Create(_ context.Context, a *model.Avatar) (*model.Avatar, error) { return a, nil }
func (f *fakeRepo) Get(_ context.Context, _ int64, id uuid.UUID) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok {
		return nil, storage.ErrAvatarNotFound
	}
	return a, nil
}
func (f *fakeRepo) ListByUser(context.Context, int64) ([]*model.Avatar, error) { return nil, nil }
func (f *fakeRepo) Delete(context.Context, int64, uuid.UUID) error             { return nil }
func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID,
	status model.AvatarStatus, processedKey, errMsg *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, status)
	if a, ok := f.items[id]; ok {
		a.Status = status
		a.ProcessedKey = processedKey
		a.Error = errMsg
	}
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

// tinyJPEG возвращает валидный 4x4 JPEG (декодируется imaging.Decode).
func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

// ---------- tests ----------

func TestProcessor_Handle_Ready(t *testing.T) {
	repo := newFakeRepo()
	s3 := newFakeS3()
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: 1, Status: model.AvatarPending, OriginalKey: "original/x"}
	s3.objects["original/x"] = tinyJPEG(t)

	p := NewProcessor(repo, s3)
	err := p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	})
	require.NoError(t, err)

	require.Equal(t, []model.AvatarStatus{model.AvatarProcessing, model.AvatarReady}, repo.statuses)
	require.Contains(t, s3.objects, "processed/"+id.String())
}

func TestProcessor_Handle_S3GetFails_MarkFailed(t *testing.T) {
	repo := newFakeRepo()
	s3 := newFakeS3()
	s3.getErr = errors.New("s3 down")
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: 1, Status: model.AvatarPending}

	p := NewProcessor(repo, s3)
	err := p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	})
	require.Error(t, err)
	require.Equal(t, []model.AvatarStatus{model.AvatarProcessing, model.AvatarFailed}, repo.statuses)
	require.NotNil(t, repo.items[id].Error)
}

func TestProcessor_Handle_DecodeFails_MarkFailed(t *testing.T) {
	repo := newFakeRepo()
	s3 := newFakeS3()
	id := uuid.New()
	repo.items[id] = &model.Avatar{ID: id, UserID: 1, Status: model.AvatarPending}
	s3.objects["original/x"] = []byte("not-an-image")

	p := NewProcessor(repo, s3)
	err := p.Handle(context.Background(), broker.AvatarTask{
		AvatarID: id.String(), OriginalKey: "original/x",
	})
	require.Error(t, err)
	require.Equal(t, []model.AvatarStatus{model.AvatarProcessing, model.AvatarFailed}, repo.statuses)
}

func TestProcessor_Handle_BadUUID(t *testing.T) {
	p := NewProcessor(newFakeRepo(), newFakeS3())
	err := p.Handle(context.Background(), broker.AvatarTask{AvatarID: "not-uuid"})
	require.Error(t, err)
}

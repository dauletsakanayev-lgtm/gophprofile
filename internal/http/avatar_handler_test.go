package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/broker"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ---------- fakes ----------

type fakeRepo struct {
	mu    sync.Mutex
	items map[uuid.UUID]*model.Avatar
	// controls:
	createErr error
	getErr    error
	listErr   error
	delErr    error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{items: map[uuid.UUID]*model.Avatar{}} }

func (f *fakeRepo) Create(_ context.Context, a *model.Avatar) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	a.Status = model.AvatarPending
	f.items[a.ID] = a
	return a, nil
}

func (f *fakeRepo) Get(_ context.Context, userID int64, id uuid.UUID) (*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	a, ok := f.items[id]
	if !ok || a.UserID != userID {
		return nil, storage.ErrAvatarNotFound
	}
	return a, nil
}

func (f *fakeRepo) ListByUser(_ context.Context, userID int64) ([]*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*model.Avatar
	for _, a := range f.items {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID,
	status model.AvatarStatus, processedKey, errMsg *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok {
		return storage.ErrAvatarNotFound
	}
	a.Status = status
	a.ProcessedKey = processedKey
	a.Error = errMsg
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, userID int64, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delErr != nil {
		return f.delErr
	}
	a, ok := f.items[id]
	if !ok || a.UserID != userID {
		return storage.ErrAvatarNotFound
	}
	delete(f.items, id)
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
	tasks []broker.AvatarTask
	err   error
}

func (p *fakePub) Publish(_ context.Context, t broker.AvatarTask) error {
	if p.err != nil {
		return p.err
	}
	p.tasks = append(p.tasks, t)
	return nil
}

// ---------- helpers ----------

func newTestRouter(t *testing.T, ah *AvatarHandler) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1/avatars", func(r chi.Router) {
		r.Use(AuthMiddleware)
		r.Post("/", ah.Create)
		r.Get("/", ah.List)
		r.Get("/{id}", ah.Get)
		r.Get("/{id}/original", ah.DownloadOriginal)
		r.Get("/{id}/processed", ah.DownloadProcessed)
		r.Delete("/{id}", ah.Delete)
	})
	return r
}

func uploadRequest(t *testing.T, userID string, contentType string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Type"] = []string{contentType}
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="a.jpg"`}
	part, err := w.CreatePart(h)
	require.NoError(t, err)
	_, _ = part.Write(data)
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	return req
}

// ---------- tests ----------

func TestHandler_Create_OK(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "1", "image/jpeg", []byte("fake-jpeg-bytes")))

	require.Equal(t, http.StatusCreated, rr.Code)

	var a model.Avatar
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &a))
	require.Equal(t, int64(1), a.UserID)
	require.Equal(t, model.AvatarPending, a.Status)
	require.Contains(t, a.OriginalKey, "original/")
	require.Len(t, pub.tasks, 1)
	require.Len(t, s3.objects, 1)
}

func TestHandler_Create_NoAuth(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "", "image/jpeg", []byte("x")))
	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Empty(t, pub.tasks)
}

func TestHandler_Create_BadMIME(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "1", "application/pdf", []byte("x")))
	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	require.Empty(t, pub.tasks)
	require.Empty(t, s3.objects)
}

func TestHandler_Create_S3PutFails_NoDBInsert(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	s3.putErr = errors.New("boom")
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "1", "image/jpeg", []byte("x")))

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Empty(t, repo.items)
	require.Empty(t, pub.tasks)
}

func TestHandler_Create_DBFails_S3Compensated(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	repo.createErr = errors.New("db down")
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "1", "image/jpeg", []byte("x")))

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Empty(t, s3.objects, "S3 object must be compensated on DB failure")
	require.Empty(t, pub.tasks)
}

func TestHandler_List_Empty(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars", nil)
	req.Header.Set("X-User-ID", "1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "[]\n", rr.Body.String())
}

func TestHandler_Get_NotFound(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+uuid.NewString(), nil)
	req.Header.Set("X-User-ID", "1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandler_Get_InvalidID(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/not-a-uuid", nil)
	req.Header.Set("X-User-ID", "1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandler_DownloadProcessed_NotReady(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	id := uuid.New()
	repo.items[id] = &model.Avatar{
		ID: id, UserID: 1, Status: model.AvatarPending,
		OriginalKey: "original/x", ContentType: "image/jpeg",
	}
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"/processed", nil)
	req.Header.Set("X-User-ID", "1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "processed version not ready")
}

func TestHandler_Delete_OK(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	id := uuid.New()
	repo.items[id] = &model.Avatar{
		ID: id, UserID: 1, Status: model.AvatarPending,
		OriginalKey: "original/x",
	}
	s3.objects["original/x"] = []byte("data")
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	req.Header.Set("X-User-ID", "1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Empty(t, repo.items)
	require.Empty(t, s3.objects)
}

func TestHandler_DownloadOriginal_OK(t *testing.T) {
	repo, s3, pub := newFakeRepo(), newFakeS3(), &fakePub{}
	id := uuid.New()
	repo.items[id] = &model.Avatar{
		ID: id, UserID: 1, Status: model.AvatarPending,
		OriginalKey: "original/x", ContentType: "image/jpeg",
	}
	s3.objects["original/x"] = []byte("bytes-content")
	h := newTestRouter(t, NewAvatarHandler(repo, s3, pub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"/original", nil)
	req.Header.Set("X-User-ID", "1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "image/jpeg", rr.Header().Get("Content-Type"))
	require.Equal(t, "bytes-content", rr.Body.String())
}

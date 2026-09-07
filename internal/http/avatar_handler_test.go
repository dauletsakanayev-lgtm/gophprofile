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
	"strings"
	"sync"
	"testing"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/service"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ---------- fake service ----------

type fakeSvc struct {
	mu    sync.Mutex
	items map[uuid.UUID]*model.Avatar

	uploadErr    error
	getErr       error
	openErr      error
	deleteErr    error
	deleteByErr  error
	openContent  string
	openCT       string
	uploaded     []service.UploadCmd
	softDeleted  []uuid.UUID
	byUserResult *model.Avatar
}

func newFakeSvc() *fakeSvc { return &fakeSvc{items: map[uuid.UUID]*model.Avatar{}} }

func (f *fakeSvc) Upload(_ context.Context, cmd service.UploadCmd) (*model.Avatar, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	// Сдренируем reader (в реальном service это делает S3-адаптер).
	_, _ = io.Copy(io.Discard, cmd.Reader)
	id := uuid.New()
	a := &model.Avatar{
		ID: id, UserID: cmd.UserID, FileName: cmd.FileName,
		MimeType: cmd.MimeType, SizeBytes: cmd.SizeBytes,
		S3Key: "original/" + id.String(),
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded = append(f.uploaded, cmd)
	f.items[id] = a
	return a, nil
}
func (f *fakeSvc) Get(_ context.Context, id uuid.UUID) (*model.Avatar, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok {
		return nil, storage.ErrAvatarNotFound
	}
	return a, nil
}
func (f *fakeSvc) GetByUser(_ context.Context, userID string) (*model.Avatar, error) {
	if f.byUserResult != nil {
		return f.byUserResult, nil
	}
	return nil, storage.ErrAvatarNotFound
}
func (f *fakeSvc) ListByUser(_ context.Context, userID string) ([]*model.Avatar, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*model.Avatar
	for _, a := range f.items {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeSvc) OpenFile(_ context.Context, _ *model.Avatar, _ string) (io.ReadCloser, string, error) {
	if f.openErr != nil {
		return nil, "", f.openErr
	}
	ct := f.openCT
	if ct == "" {
		ct = "image/jpeg"
	}
	return io.NopCloser(strings.NewReader(f.openContent)), ct, nil
}
func (f *fakeSvc) Delete(_ context.Context, id uuid.UUID, _ string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.softDeleted = append(f.softDeleted, id)
	delete(f.items, id)
	return nil
}
func (f *fakeSvc) DeleteByUser(_ context.Context, _, _ string) error {
	return f.deleteByErr
}

// ---------- helpers ----------

func newTestRouter(t *testing.T, ah *AvatarHandler) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/avatars/{id}", ah.Get)
		r.Get("/avatars/{id}/metadata", ah.GetMetadata)
		r.Get("/users/{user_id}/avatar", ah.GetUserAvatar)
		r.Get("/users/{user_id}/avatars", ah.ListUserAvatars)
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware)
			r.Post("/avatars", ah.Create)
			r.Delete("/avatars/{id}", ah.Delete)
			r.Delete("/users/{user_id}/avatar", ah.DeleteUserAvatar)
		})
	})
	return r
}

func uploadRequest(t *testing.T, userID, contentType, fieldName string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := map[string][]string{
		"Content-Type":        {contentType},
		"Content-Disposition": {`form-data; name="` + fieldName + `"; filename="a.jpg"`},
	}
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

func TestHandler_Create_OK_Image(t *testing.T) {
	svc := newFakeSvc()
	h := newTestRouter(t, NewAvatarHandler(svc))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "alice", "image/jpeg", "image", []byte("data")))
	require.Equal(t, http.StatusCreated, rr.Code)
	require.Len(t, svc.uploaded, 1)
	require.Equal(t, "alice", svc.uploaded[0].UserID)
}

func TestHandler_Create_OK_File(t *testing.T) {
	svc := newFakeSvc()
	h := newTestRouter(t, NewAvatarHandler(svc))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "alice", "image/jpeg", "file", []byte("data")))
	require.Equal(t, http.StatusCreated, rr.Code)
}

func TestHandler_Create_NoAuth(t *testing.T) {
	svc := newFakeSvc()
	h := newTestRouter(t, NewAvatarHandler(svc))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "", "image/jpeg", "image", []byte("x")))
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandler_Create_UnsupportedMIME(t *testing.T) {
	svc := newFakeSvc()
	svc.uploadErr = service.ErrUnsupportedMIME
	h := newTestRouter(t, NewAvatarHandler(svc))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "u", "image/jpeg", "image", []byte("x")))
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "Invalid file format")
}

func TestHandler_Create_TooLarge(t *testing.T) {
	svc := newFakeSvc()
	svc.uploadErr = service.ErrFileTooLarge
	h := newTestRouter(t, NewAvatarHandler(svc))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, uploadRequest(t, "u", "image/jpeg", "image", []byte("x")))
	require.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	require.Contains(t, rr.Body.String(), "File too large")
}

func TestHandler_Get_Binary(t *testing.T) {
	svc := newFakeSvc()
	id := uuid.New()
	svc.items[id] = &model.Avatar{ID: id, MimeType: "image/jpeg"}
	svc.openContent = "raw-bytes"
	svc.openCT = "image/jpeg"

	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"?size=original", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "image/jpeg", rr.Header().Get("Content-Type"))
	require.Equal(t, "raw-bytes", rr.Body.String())
}

func TestHandler_Get_NotReady(t *testing.T) {
	svc := newFakeSvc()
	id := uuid.New()
	svc.items[id] = &model.Avatar{ID: id}
	svc.openErr = service.ErrThumbnailNotReady

	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"?size=100x100", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "not ready")
}

func TestHandler_Get_InvalidID(t *testing.T) {
	svc := newFakeSvc()
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/bogus", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandler_GetMetadata_OK(t *testing.T) {
	svc := newFakeSvc()
	id := uuid.New()
	svc.items[id] = &model.Avatar{
		ID: id, UserID: "u", FileName: "p.jpg",
		MimeType: "image/jpeg", SizeBytes: 100,
		UploadStatus: model.UploadUploaded, ProcessingStatus: model.ProcessingCompleted,
		ThumbnailKeys: map[string]string{"100x100": "th/100.jpg"},
	}
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+id.String()+"/metadata", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Equal(t, "p.jpg", resp["file_name"])
	require.NotEmpty(t, resp["thumbnails"])
}

func TestHandler_GetMetadata_NotFound(t *testing.T) {
	svc := newFakeSvc()
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+uuid.NewString()+"/metadata", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandler_GetUserAvatar(t *testing.T) {
	svc := newFakeSvc()
	svc.byUserResult = &model.Avatar{UserID: "alice", MimeType: "image/jpeg"}
	svc.openContent = "alice-bytes"
	svc.openCT = "image/jpeg"

	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/alice/avatar", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "alice-bytes", rr.Body.String())
}

func TestHandler_ListUserAvatars(t *testing.T) {
	svc := newFakeSvc()
	id := uuid.New()
	svc.items[id] = &model.Avatar{ID: id, UserID: "alice", FileName: "a.jpg"}
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/alice/avatars", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var arr []map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &arr))
	require.Len(t, arr, 1)
	require.Equal(t, "a.jpg", arr[0]["file_name"])
}

func TestHandler_Delete_OK(t *testing.T) {
	svc := newFakeSvc()
	id := uuid.New()
	svc.items[id] = &model.Avatar{ID: id, UserID: "u"}
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	req.Header.Set("X-User-ID", "u")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Contains(t, svc.softDeleted, id)
}

func TestHandler_Delete_Forbidden(t *testing.T) {
	svc := newFakeSvc()
	svc.deleteErr = service.ErrForbidden
	id := uuid.New()
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	req.Header.Set("X-User-ID", "bob")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

func TestHandler_Delete_NotFound(t *testing.T) {
	svc := newFakeSvc()
	svc.deleteErr = storage.ErrAvatarNotFound
	id := uuid.New()
	h := newTestRouter(t, NewAvatarHandler(svc))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+id.String(), nil)
	req.Header.Set("X-User-ID", "u")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// защита от неявного использования errors в тестах
var _ = errors.New

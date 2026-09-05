package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	maxUploadBytes = 5 << 20 // 5 MB
	fieldName      = "file"
)

var allowedMIME = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

// AvatarHandler — REST-хендлер для операций с аватарами.
type AvatarHandler struct {
	repo storage.AvatarRepository
	s3   *storage.S3Store
}

func NewAvatarHandler(repo storage.AvatarRepository, s3 *storage.S3Store) *AvatarHandler {
	return &AvatarHandler{repo: repo, s3: s3}
}

// Create — POST /api/v1/avatars.
// multipart-форма с полем "file", загружает оригинал в S3 и создаёт запись в БД.
func (h *AvatarHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile(fieldName)
	if err != nil {
		http.Error(w, "expected form field 'file': "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	ct := header.Header.Get("Content-Type")
	if _, ok := allowedMIME[ct]; !ok {
		http.Error(w, "unsupported content type: "+ct, http.StatusUnsupportedMediaType)
		return
	}

	id := uuid.New()
	key := "original/" + id.String()

	if err := h.s3.Put(r.Context(), key, file, header.Size, ct); err != nil {
		http.Error(w, "s3 put failed", http.StatusInternalServerError)
		return
	}

	created, err := h.repo.Create(r.Context(), &model.Avatar{
		ID:          id,
		UserID:      userID,
		OriginalKey: key,
		ContentType: ct,
		SizeBytes:   header.Size,
	})
	if err != nil {
		// Компенсация: удаляем осиротевший объект из S3
		_ = h.s3.Delete(r.Context(), key)
		http.Error(w, "db insert failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// List — GET /api/v1/avatars.
func (h *AvatarHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.repo.ListByUser(r.Context(), userIDFromCtx(r.Context()))
	if err != nil {
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []*model.Avatar{}
	}
	writeJSON(w, http.StatusOK, items)
}

// Get — GET /api/v1/avatars/{id}.
func (h *AvatarHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	a, err := h.repo.Get(r.Context(), userIDFromCtx(r.Context()), id)
	if errors.Is(err, storage.ErrAvatarNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// DownloadOriginal — GET /api/v1/avatars/{id}/original.
func (h *AvatarHandler) DownloadOriginal(w http.ResponseWriter, r *http.Request) {
	h.download(w, r, "original")
}

// DownloadProcessed — GET /api/v1/avatars/{id}/processed.
func (h *AvatarHandler) DownloadProcessed(w http.ResponseWriter, r *http.Request) {
	h.download(w, r, "processed")
}

func (h *AvatarHandler) download(w http.ResponseWriter, r *http.Request, which string) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	a, err := h.repo.Get(r.Context(), userIDFromCtx(r.Context()), id)
	if errors.Is(err, storage.ErrAvatarNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}

	var key string
	switch which {
	case "original":
		key = a.OriginalKey
	case "processed":
		if a.ProcessedKey == nil {
			http.Error(w, "processed version not ready", http.StatusNotFound)
			return
		}
		key = *a.ProcessedKey
	}

	obj, err := h.s3.Get(r.Context(), key)
	if errors.Is(err, storage.ErrObjectNotFound) {
		http.Error(w, "object missing in storage", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "s3 get failed", http.StatusInternalServerError)
		return
	}
	defer obj.Close()

	w.Header().Set("Content-Type", a.ContentType)
	_, _ = io.Copy(w, obj)
}

// Delete — DELETE /api/v1/avatars/{id}.
func (h *AvatarHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	// Сначала получаем ключи, чтобы потом почистить S3.
	a, err := h.repo.Get(r.Context(), userID, id)
	if errors.Is(err, storage.ErrAvatarNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db query failed", http.StatusInternalServerError)
		return
	}

	if err := h.repo.Delete(r.Context(), userID, id); err != nil {
		http.Error(w, "db delete failed", http.StatusInternalServerError)
		return
	}

	// Best-effort: если упадёт — объекты в S3 осиротеют (потом почистим отдельным job'ом).
	_ = h.s3.Delete(r.Context(), a.OriginalKey)
	if a.ProcessedKey != nil {
		_ = h.s3.Delete(r.Context(), *a.ProcessedKey)
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

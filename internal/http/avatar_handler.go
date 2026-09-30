package http

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/dauletsakanayev-lgtm/gophprofile/internal/model"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/service"
	"github.com/dauletsakanayev-lgtm/gophprofile/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// fieldNames — оба имени поля multipart: "image" (SPA от Practicum), "file" (по ТЗ).
var fieldNames = []string{"image", "file"}

// AvatarHandler — HTTP-транспорт. Всю бизнес-логику делегирует service.
type AvatarHandler struct {
	svc service.AvatarService
}

func NewAvatarHandler(svc service.AvatarService) *AvatarHandler {
	return &AvatarHandler{svc: svc}
}

// Create — POST /api/v1/avatars.
func (h *AvatarHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxUploadBytes)

	var (
		file   multipart.File
		header *multipart.FileHeader
		err    error
	)
	for _, name := range fieldNames {
		file, header, err = r.FormFile(name)
		if err == nil {
			break
		}
	}
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest,
			"Expected form field 'image' or 'file'", err.Error())
		return
	}
	defer file.Close()

	created, err := h.svc.Upload(r.Context(), service.UploadCmd{
		UserID:    userID,
		FileName:  header.Filename,
		MimeType:  header.Header.Get("Content-Type"),
		SizeBytes: header.Size,
		Reader:    file,
	})
	if err != nil {
		writeServiceErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         created.ID,
		"user_id":    created.UserID,
		"url":        "/api/v1/avatars/" + created.ID.String(),
		"status":     "processing",
		"created_at": created.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// Get — GET /api/v1/avatars/{id}?size=original|100x100|300x300.
func (h *AvatarHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "Invalid id", err.Error())
		return
	}
	a, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	h.stream(w, r, a)
}

// GetMetadata — GET /api/v1/avatars/{id}/metadata.
func (h *AvatarHandler) GetMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "Invalid id", err.Error())
		return
	}
	a, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, metadataResponse(a))
}

// GetUserAvatar — GET /api/v1/users/{user_id}/avatar (бинарь последнего active).
func (h *AvatarHandler) GetUserAvatar(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.GetByUser(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	h.stream(w, r, a)
}

// ListUserAvatars — GET /api/v1/users/{user_id}/avatars.
func (h *AvatarHandler) ListUserAvatars(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListByUser(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	out := make([]any, 0, len(items))
	for _, a := range items {
		out = append(out, metadataResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete — DELETE /api/v1/avatars/{id}.
func (h *AvatarHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "Invalid id", err.Error())
		return
	}
	if err := h.svc.Delete(r.Context(), id, userIDFromCtx(r.Context())); err != nil {
		writeServiceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteUserAvatar — DELETE /api/v1/users/{user_id}/avatar.
func (h *AvatarHandler) DeleteUserAvatar(w http.ResponseWriter, r *http.Request) {
	err := h.svc.DeleteByUser(r.Context(),
		chi.URLParam(r, "user_id"), userIDFromCtx(r.Context()))
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stream отдаёт бинарь картинки по запрошенному size (query-параметр).
func (h *AvatarHandler) stream(w http.ResponseWriter, r *http.Request, a *model.Avatar) {
	obj, ct, err := h.svc.OpenFile(r.Context(), a, r.URL.Query().Get("size"))
	if err != nil {
		writeServiceErr(w, err)
		return
	}
	defer obj.Close()
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = io.Copy(w, obj)
}

// writeServiceErr переводит бизнес-ошибки в HTTP-статусы.
func writeServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrAvatarNotFound):
		writeJSONErr(w, http.StatusNotFound, "Avatar not found", "")
	case errors.Is(err, storage.ErrObjectNotFound):
		writeJSONErr(w, http.StatusNotFound, "Object missing in storage", "")
	case errors.Is(err, service.ErrForbidden):
		writeJSONMap(w, http.StatusForbidden, map[string]any{
			"error":   "Forbidden",
			"details": "You can only delete your own avatars",
		})
	case errors.Is(err, service.ErrUnsupportedMIME):
		writeJSONMap(w, http.StatusBadRequest, map[string]any{
			"error":   "Invalid file format",
			"details": "Supported formats: jpeg, png, webp",
		})
	case errors.Is(err, service.ErrFileTooLarge):
		writeJSONMap(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error":    "File too large",
			"max_size": service.MaxUploadBytes,
		})
	case errors.Is(err, service.ErrThumbnailNotReady):
		writeJSONErr(w, http.StatusNotFound, "Thumbnail not ready", "processing in progress")
	case errors.Is(err, service.ErrThumbnailNotFound):
		writeJSONErr(w, http.StatusNotFound, "Thumbnail not found", "")
	case errors.Is(err, service.ErrInvalidSize):
		writeJSONErr(w, http.StatusBadRequest, "Invalid size",
			"allowed: original, 100x100, 300x300")
	default:
		log.Printf("internal error: %v", err)
		writeJSONErr(w, http.StatusInternalServerError, "Internal server error", "")
	}
}

// metadataResponse формирует JSON-метаданные по формату ТЗ.
func metadataResponse(a *model.Avatar) map[string]any {
	thumbs := make([]map[string]string, 0, len(a.ThumbnailKeys))
	for size, key := range a.ThumbnailKeys {
		thumbs = append(thumbs, map[string]string{
			"size": size,
			"url":  "/api/v1/avatars/" + a.ID.String() + "?size=" + size,
			"key":  key,
		})
	}
	resp := map[string]any{
		"id":                a.ID,
		"user_id":           a.UserID,
		"file_name":         a.FileName,
		"mime_type":         a.MimeType,
		"size":              a.SizeBytes,
		"upload_status":     a.UploadStatus,
		"processing_status": a.ProcessingStatus,
		"thumbnails":        thumbs,
		"created_at":        a.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":        a.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if a.Width != nil && a.Height != nil {
		resp["dimensions"] = map[string]int{"width": *a.Width, "height": *a.Height}
	}
	return resp
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONMap(w http.ResponseWriter, status int, m map[string]any) {
	writeJSON(w, status, m)
}

func writeJSONErr(w http.ResponseWriter, status int, msg, details string) {
	m := map[string]any{"error": msg}
	if details != "" {
		m["details"] = details
	}
	writeJSON(w, status, m)
}

// WebUpload — POST /web/upload. Форма шлёт multipart с user_id + file/image.
// Аналог Create, но user_id читается не из header, а из поля формы.
func (h *AvatarHandler) WebUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxUploadBytes)

	// Извлекаем user_id из формы (поддерживаем оба варианта имени).
	if err := r.ParseMultipartForm(service.MaxUploadBytes); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "Invalid form", err.Error())
		return
	}
	userID := r.FormValue("user_id")
	if userID == "" {
		userID = r.FormValue("userId")
	}
	if userID == "" || len(userID) > 255 {
		writeJSONErr(w, http.StatusBadRequest,
			"user_id form field required (1-255 chars)", "")
		return
	}

	var (
		file   multipart.File
		header *multipart.FileHeader
		err    error
	)
	for _, name := range fieldNames {
		file, header, err = r.FormFile(name)
		if err == nil {
			break
		}
	}
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest,
			"Expected form field 'image' or 'file'", err.Error())
		return
	}
	defer file.Close()

	created, err := h.svc.Upload(r.Context(), service.UploadCmd{
		UserID:    userID,
		FileName:  header.Filename,
		MimeType:  header.Header.Get("Content-Type"),
		SizeBytes: header.Size,
		Reader:    file,
	})
	if err != nil {
		writeServiceErr(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         created.ID,
		"user_id":    created.UserID,
		"url":        "/api/v1/avatars/" + created.ID.String(),
		"status":     "processing",
		"created_at": created.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// Package model содержит доменные структуры GophProfile.
package model

import (
	"time"

	"github.com/google/uuid"
)

// UploadStatus — жизненный цикл загрузки файла.
type UploadStatus string

const (
	UploadUploading UploadStatus = "uploading"
	UploadUploaded  UploadStatus = "uploaded"
)

// ProcessingStatus — жизненный цикл обработки миниатюр.
type ProcessingStatus string

const (
	ProcessingPending    ProcessingStatus = "pending"
	ProcessingProcessing ProcessingStatus = "processing"
	ProcessingCompleted  ProcessingStatus = "completed"
	ProcessingFailed     ProcessingStatus = "failed"
)

// Avatar — метаданные аватара пользователя.
// Файлы (оригинал и миниатюры) хранятся в S3.
type Avatar struct {
	ID               uuid.UUID         `json:"id"`
	UserID           string            `json:"user_id"`
	FileName         string            `json:"file_name"`
	MimeType         string            `json:"mime_type"`
	SizeBytes        int64             `json:"size_bytes"`
	S3Key            string            `json:"s3_key"`
	ThumbnailKeys    map[string]string `json:"thumbnails,omitempty"`
	UploadStatus     UploadStatus      `json:"upload_status"`
	ProcessingStatus ProcessingStatus  `json:"processing_status"`
	Width            *int              `json:"width,omitempty"`  // ← ДОБАВИТЬ
	Height           *int              `json:"height,omitempty"` // ← ДОБАВИТЬ
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	DeletedAt        *time.Time        `json:"deleted_at,omitempty"`
}

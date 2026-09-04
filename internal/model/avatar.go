// Package model содержит доменные структуры GophProfile.
package model

import (
	"time"

	"github.com/google/uuid"
)

// AvatarStatus — статус жизненного цикла аватара.
// Значения синхронизированы с enum `avatar_status` в PostgreSQL.
type AvatarStatus string

const (
	AvatarPending    AvatarStatus = "pending"
	AvatarProcessing AvatarStatus = "processing"
	AvatarReady      AvatarStatus = "ready"
	AvatarFailed     AvatarStatus = "failed"
)

// Avatar — метаданные загруженного изображения.
// Сами файлы (оригинал и обработанная версия) лежат в S3/MinIO
// по ключам OriginalKey / ProcessedKey.
type Avatar struct {
	ID           uuid.UUID    `json:"id"`
	UserID       int64        `json:"user_id"`
	Status       AvatarStatus `json:"status"`
	OriginalKey  string       `json:"original_key"`
	ProcessedKey *string      `json:"processed_key,omitempty"`
	ContentType  string       `json:"content_type"`
	SizeBytes    int64        `json:"size_bytes"`
	Error        *string      `json:"error,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

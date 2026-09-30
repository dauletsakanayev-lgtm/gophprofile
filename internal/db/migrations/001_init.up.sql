-- Расширение для gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Таблица avatars — как в ТЗ спринта 1
CREATE TABLE avatars (
    id                UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           VARCHAR(255)   NOT NULL,
    file_name         VARCHAR(255)   NOT NULL,
    mime_type         VARCHAR(100)   NOT NULL,
    size_bytes        BIGINT         NOT NULL CHECK (size_bytes > 0),
    s3_key            VARCHAR(500)   NOT NULL,
    thumbnail_s3_keys JSONB,
    upload_status     VARCHAR(50)    NOT NULL DEFAULT 'uploading',
    processing_status VARCHAR(50)    NOT NULL DEFAULT 'pending',
    created_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ
);

-- Индексы по ТЗ
CREATE INDEX idx_avatars_user_id
    ON avatars(user_id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_avatars_status
    ON avatars(upload_status, processing_status);
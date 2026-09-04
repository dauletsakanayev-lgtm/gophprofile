-- Расширение для gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Статус жизненного цикла аватара
CREATE TYPE avatar_status AS ENUM (
    'pending',      -- загружен, ждёт worker
    'processing',   -- worker обрабатывает
    'ready',        -- готов, processed_key заполнен
    'failed'        -- ошибка (см. поле error)
);

CREATE TABLE avatars (
    id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        BIGINT         NOT NULL,
    status         avatar_status  NOT NULL DEFAULT 'pending',
    original_key   TEXT           NOT NULL,        -- S3-ключ оригинала
    processed_key  TEXT,                           -- S3-ключ после обработки (nullable)
    content_type   TEXT           NOT NULL,        -- image/jpeg, image/png, ...
    size_bytes     BIGINT         NOT NULL CHECK (size_bytes > 0),
    error          TEXT,                           -- заполняется при status=failed
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_avatars_user_id ON avatars(user_id);

-- Частичный индекс: worker берёт задачи по этим статусам
CREATE INDEX idx_avatars_pending
    ON avatars(created_at)
    WHERE status IN ('pending', 'processing');
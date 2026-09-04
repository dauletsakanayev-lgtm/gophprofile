DROP INDEX IF EXISTS idx_avatars_pending;
DROP INDEX IF EXISTS idx_avatars_user_id;
DROP TABLE IF EXISTS avatars;
DROP TYPE IF EXISTS avatar_status;
-- pgcrypto оставляем — может использоваться другими таблицами
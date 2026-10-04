-- Telegram file IDs of finished downloads, so the same content can be sent
-- again instantly without downloading or uploading.
CREATE TABLE IF NOT EXISTS media_cache (
    cache_key TEXT PRIMARY KEY,
    items JSONB NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    performer TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    hits INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_media_cache_last_used ON media_cache(last_used_at);

ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS cache_key TEXT NOT NULL DEFAULT '';
-- Delivered from the cache without downloading; excluded from daily quotas.
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS cached BOOLEAN NOT NULL DEFAULT false;
-- Set for inline-mode requests: the shared message to update with the media.
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS inline_message_id TEXT NOT NULL DEFAULT '';

-- Consolidates everything EnsureSchema/EnsurePreferencesTable used to create at
-- startup, so all schema lives in versioned migrations. Every statement is
-- idempotent because existing deployments already have some of it.

ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS progress_text TEXT NOT NULL DEFAULT '';
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS progress_percent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS file_size_bytes BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS banned_users (
    user_id BIGINT PRIMARY KEY,
    banned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id BIGINT PRIMARY KEY,
    quality TEXT NOT NULL DEFAULT 'best',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Download mode and quality used to be encoded as a URL prefix
-- ("audio:<url>", "q720:<url>"). Give them real columns.
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'video';
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS quality TEXT NOT NULL DEFAULT '';

UPDATE download_jobs SET mode = 'audio', url = substr(url, length('audio:') + 1)
    WHERE url LIKE 'audio:%';
UPDATE download_jobs
    SET quality = split_part(url, ':', 1), url = substr(url, length(split_part(url, ':', 1)) + 2)
    WHERE url ~ '^(q360|q480|q720|q1080|telegram):';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'download_jobs_status_check') THEN
        ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_status_check
            CHECK (status IN ('pending', 'processing', 'done', 'failed', 'canceled'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'download_jobs_mode_check') THEN
        ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_mode_check
            CHECK (mode IN ('video', 'audio'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_download_jobs_status_priority_created_at
    ON download_jobs(status, priority DESC, created_at);
CREATE INDEX IF NOT EXISTS idx_download_jobs_user_status ON download_jobs(user_id, status);
CREATE INDEX IF NOT EXISTS idx_download_jobs_user_created ON download_jobs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_download_jobs_finished_at ON download_jobs(finished_at)
    WHERE finished_at IS NOT NULL;

-- Everyone who has talked to the bot. Broadcasts and /users read this
-- instead of download_jobs, which is pruned by the cleanup loop.
CREATE TABLE IF NOT EXISTS users (
    user_id BIGINT PRIMARY KEY,
    chat_id BIGINT NOT NULL DEFAULT 0,
    username TEXT NOT NULL DEFAULT '',
    first_name TEXT NOT NULL DEFAULT '',
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set when Telegram says the user blocked the bot; cleared on next contact.
    blocked_bot BOOLEAN NOT NULL DEFAULT false
);

INSERT INTO users (user_id, chat_id, first_seen, last_seen)
SELECT user_id,
       (array_agg(chat_id ORDER BY (chat_id = user_id) DESC, created_at DESC))[1],
       min(created_at),
       max(created_at)
FROM download_jobs
WHERE user_id > 0
GROUP BY user_id
ON CONFLICT (user_id) DO NOTHING;

-- Aggregates of jobs removed by the cleanup loop, so stats, history totals
-- and bandwidth reports survive pruning.
CREATE TABLE IF NOT EXISTS job_stats_daily (
    day DATE NOT NULL,
    user_id BIGINT NOT NULL,
    platform TEXT NOT NULL,
    status TEXT NOT NULL,
    jobs BIGINT NOT NULL DEFAULT 0,
    bytes BIGINT NOT NULL DEFAULT 0,
    last_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (day, user_id, platform, status)
);
CREATE INDEX IF NOT EXISTS idx_job_stats_daily_user ON job_stats_daily(user_id);

CREATE TABLE IF NOT EXISTS download_jobs (
    id BIGSERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL DEFAULT 0,
    url TEXT NOT NULL,
    platform TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    output_path TEXT NOT NULL DEFAULT '',
    output_name TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0,
    telegram_message_id BIGINT NOT NULL DEFAULT 0,
    progress_text TEXT NOT NULL DEFAULT '',
    progress_percent INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_download_jobs_status_priority_created_at
    ON download_jobs(status, priority DESC, created_at);

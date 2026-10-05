-- Channels/playlists a chat follows; new uploads are queued automatically.
CREATE TABLE IF NOT EXISTS subscriptions (
    id BIGSERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    url TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'video' CHECK (mode IN ('video', 'audio')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    failures INTEGER NOT NULL DEFAULT 0,
    UNIQUE (chat_id, url)
);
CREATE INDEX IF NOT EXISTS idx_subscriptions_next_check ON subscriptions(next_check_at);

-- Entries already delivered (or present when the subscription started).
CREATE TABLE IF NOT EXISTS subscription_seen (
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    entry_id TEXT NOT NULL,
    seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscription_id, entry_id)
);

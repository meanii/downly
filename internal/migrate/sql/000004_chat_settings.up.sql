-- Per-chat preferences. For private chats chat_id equals the user's ID, so
-- this covers both users and groups. language is validated in code so new
-- languages need no migration.
CREATE TABLE IF NOT EXISTS chat_settings (
    chat_id BIGINT PRIMARY KEY,
    language TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

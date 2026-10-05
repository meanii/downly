-- A group can pick how links are handled before choosing a language, so
-- language becomes optional (NULL = not chosen yet).
ALTER TABLE chat_settings ALTER COLUMN language DROP NOT NULL;
-- 'auto': download every link posted; 'command': only via /dl.
ALTER TABLE chat_settings ADD COLUMN IF NOT EXISTS group_mode TEXT NOT NULL DEFAULT 'auto';

-- The message a job answers (the one with the link), for threaded replies.
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS reply_to_message_id BIGINT NOT NULL DEFAULT 0;

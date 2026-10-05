-- Optional section of the video to download, in seconds (both 0 = whole).
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS clip_start INTEGER NOT NULL DEFAULT 0;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS clip_end INTEGER NOT NULL DEFAULT 0;

ALTER TABLE download_jobs DROP CONSTRAINT IF EXISTS download_jobs_mode_check;
ALTER TABLE download_jobs ADD CONSTRAINT download_jobs_mode_check CHECK (mode IN ('video', 'audio', 'gif'));

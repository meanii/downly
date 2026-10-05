-- Workers refresh heartbeat_at while processing; the reaper requeues jobs
-- whose heartbeat goes stale instead of guessing from started_at.
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS worker_id TEXT NOT NULL DEFAULT '';

-- Retries wait until next_attempt_at (exponential backoff).
ALTER TABLE download_jobs ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();

DROP INDEX IF EXISTS idx_download_jobs_status_priority_created_at;
CREATE INDEX IF NOT EXISTS idx_download_jobs_claim
    ON download_jobs(priority DESC, created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_download_jobs_processing_heartbeat
    ON download_jobs(heartbeat_at) WHERE status = 'processing';

-- Wake idle workers immediately when work becomes available.
CREATE OR REPLACE FUNCTION downly_notify_job() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('downly_jobs', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS download_jobs_notify ON download_jobs;
CREATE TRIGGER download_jobs_notify
    AFTER INSERT OR UPDATE OF status ON download_jobs
    FOR EACH ROW WHEN (NEW.status = 'pending')
    EXECUTE FUNCTION downly_notify_job();

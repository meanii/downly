ALTER TABLE users ADD COLUMN IF NOT EXISTS premium_until TIMESTAMPTZ;

-- Telegram Stars payments. The charge ID makes recording idempotent and is
-- what refunds need.
CREATE TABLE IF NOT EXISTS payments (
    charge_id TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    amount INTEGER NOT NULL,
    currency TEXT NOT NULL,
    payload TEXT NOT NULL,
    days INTEGER NOT NULL,
    refunded BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_payments_user ON payments(user_id);

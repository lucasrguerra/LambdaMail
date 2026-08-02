ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS signature TEXT DEFAULT '';
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS auto_save_drafts BOOLEAN DEFAULT true;

CREATE TABLE IF NOT EXISTS system_config (
    key VARCHAR(64) PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO system_config (key, value)
VALUES ('rspamd_thresholds', '{"greylist": 4.0, "add_header": 6.0, "reject": 15.0}')
ON CONFLICT (key) DO NOTHING;

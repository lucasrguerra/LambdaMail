-- Records one-shot data repairs so they run once rather than on every start.
--
-- The first user is the attachment flag: has_attachments was only ever set for
-- multipart messages, so a single-part attachment - a DMARC aggregate report,
-- whose whole body is a zip - was stored as having none. The fix corrects new
-- deliveries; the messages already filed need re-reading, and re-reading every
-- message on every boot is not something to do forever.
CREATE TABLE IF NOT EXISTS backfills (
    name         VARCHAR(64) PRIMARY KEY,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    detail       TEXT
);

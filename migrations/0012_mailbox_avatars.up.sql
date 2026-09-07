-- The profile photo a mailbox shows inside LambdaMail.
--
-- Stored in the database rather than the blob spool: an avatar is small,
-- there is at most one per mailbox, and it is read on nearly every screen -
-- so it belongs where the mailbox row already is, and is deleted with it.
--
-- Not to be confused with BIMI, which is one logo for a whole domain and is
-- the only thing an external mail client ever renders.
CREATE TABLE IF NOT EXISTS mailbox_avatars (
    mailbox_id   UUID PRIMARY KEY REFERENCES mailboxes(id) ON DELETE CASCADE,
    content_type VARCHAR(32) NOT NULL CHECK (content_type IN ('image/png','image/jpeg','image/webp')),
    bytes        BYTEA NOT NULL,
    -- Lets a conditional request answer 304 instead of resending the image on
    -- every screen that shows the person.
    etag         CHAR(64) NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

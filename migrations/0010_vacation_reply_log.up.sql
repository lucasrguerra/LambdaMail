-- Who has already been told the mailbox is unattended.
--
-- RFC 5230 section 4.1: an autoresponder answers a given sender at most once
-- per period. Without this every message produces its own copy of the same
-- notice - a conversation of five mails produced five - and someone replying
-- to the automatic message itself was answered again, which is how a person
-- and an autoresponder end up talking past each other.
CREATE TABLE vacation_replies (
    mailbox_id   UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    -- Lowercased, so one person is one row however they spell their address.
    recipient    VARCHAR(320) NOT NULL,
    last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (mailbox_id, recipient)
);

-- The sweep that discards rows older than any suppression period reads this.
CREATE INDEX idx_vacation_replies_sent ON vacation_replies (last_sent_at);

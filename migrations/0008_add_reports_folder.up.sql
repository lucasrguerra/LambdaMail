-- Give every existing mailbox a Reports folder.
--
-- DMARC and TLS-RPT reports are delivered to the dmarc@ and tlsrpt@ aliases
-- and are now parsed on arrival and filed here rather than into the inbox.
-- Without the folder the delivery path falls back to INBOX, which is the noise
-- this is meant to remove.
--
-- IMAP defines no special-use role for reports, so the folder is matched by
-- name and special_use stays NULL.
INSERT INTO folders (mailbox_id, name, special_use)
SELECT m.id, 'Reports', NULL
  FROM mailboxes m
 WHERE NOT EXISTS (
   SELECT 1 FROM folders f
    WHERE f.mailbox_id = m.id
      AND LOWER(f.name) = 'reports'
 );

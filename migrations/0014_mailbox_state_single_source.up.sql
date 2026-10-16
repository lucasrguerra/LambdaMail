-- One definition of "a mailbox's standard folders", and a reconciliation of the
-- cached numbers that two code paths used to keep on their own.
--
-- 1. provision_default_folders
--
-- The standard folders were created in two places that had already diverged:
-- the console's createMailbox added Reports, the CLI that creates the first
-- administrator did not, so that account had no folder for DMARC and TLS-RPT
-- reports and they fell into its INBOX. Both now call this function, so the
-- list lives here and nowhere else. It is idempotent: it adds only what is
-- missing, matching by name without regard to case.
CREATE OR REPLACE FUNCTION provision_default_folders(p_mailbox_id UUID)
RETURNS VOID
LANGUAGE sql
AS $$
    INSERT INTO folders (mailbox_id, name, special_use)
    SELECT p_mailbox_id, f.name, f.special_use
      FROM (VALUES ('INBOX',   'inbox'),
                   ('Sent',    'sent'),
                   ('Drafts',  'drafts'),
                   ('Trash',   'trash'),
                   ('Junk',    'junk'),
                   ('Archive', 'archive'),
                   -- Where delivered DMARC and TLS-RPT reports are filed. IMAP has no
                   -- special-use role for these, so it is matched by name.
                   ('Reports', NULL)) AS f(name, special_use)
     WHERE NOT EXISTS (
            SELECT 1 FROM folders x
             WHERE x.mailbox_id = p_mailbox_id
               AND x.parent_id IS NULL
               AND LOWER(x.name) = LOWER(f.name));
$$;

-- Mailboxes created before the function that lack a standard folder get it now.
SELECT provision_default_folders(id) FROM mailboxes;

-- 2. Reconcile the cached numbers with the rows.
--
-- Folder counters, and the mailbox's used bytes, are denormalised caches. IMAP
-- and the webmail each maintained them and each got something wrong (IMAP never
-- moved the unread counter; the webmail never gave the quota back), so existing
-- mailboxes carry the drift. Every path now goes through one MailStore that
-- keeps them true; this corrects what is already stored, once. It only touches
-- rows that are actually wrong, so it is safe to run again.
UPDATE folders f
   SET unread_count = c.unread,
       total_count  = c.total
  FROM (
    SELECT f2.id,
           COUNT(e.id) FILTER (
             WHERE NOT EXISTS (
               SELECT 1 FROM message_flags mf
                WHERE mf.message_id = e.id
                  AND mf.received_at = e.received_at
                  AND mf.flag = '\Seen')
           )::int AS unread,
           COUNT(e.id)::int AS total
      FROM folders f2
      LEFT JOIN email_messages e
             ON e.folder_id = f2.id AND e.expunged_at IS NULL
     GROUP BY f2.id
  ) AS c
 WHERE f.id = c.id
   AND (f.unread_count IS DISTINCT FROM c.unread
     OR f.total_count  IS DISTINCT FROM c.total);

UPDATE mailboxes m
   SET used_bytes = COALESCE(s.total, 0)
  FROM mailboxes m2
  LEFT JOIN (
    SELECT mailbox_id, SUM(size_bytes)::bigint AS total
      FROM email_messages
     WHERE expunged_at IS NULL
     GROUP BY mailbox_id
  ) AS s ON s.mailbox_id = m2.id
 WHERE m.id = m2.id
   AND m.used_bytes IS DISTINCT FROM COALESCE(s.total, 0);

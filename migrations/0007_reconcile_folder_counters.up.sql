-- Reconcile folders.unread_count and folders.total_count with the messages
-- that are actually in each folder.
--
-- These columns are a denormalised cache. Every write path maintained them,
-- but one did not: a message the user composed themselves - their own Sent
-- copy, and every draft autosave - was filed through the inbound path, which
-- raised unread_count unconditionally. Nothing ever opens your own outgoing
-- copy, so the flag that would bring the counter back down was never set and
-- the drift only accumulated.
--
-- The webmail now derives these numbers at read time and is self-healing, but
-- IMAP's STATUS answers from these columns, so the stored values are corrected
-- here once for the mailboxes that already drifted.
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
     OR f.total_count IS DISTINCT FROM c.total);

-- Existing Sent and Drafts messages were filed without the flags that say what
-- they are, so they read as unread mail that nothing could clear.
INSERT INTO message_flags (message_id, received_at, flag)
SELECT e.id, e.received_at, '\Seen'
  FROM email_messages e
  JOIN folders f ON f.id = e.folder_id
 WHERE e.expunged_at IS NULL
   AND (f.special_use IN ('sent', 'drafts')
        OR LOWER(f.name) IN ('sent', 'drafts'))
ON CONFLICT DO NOTHING;

INSERT INTO message_flags (message_id, received_at, flag)
SELECT e.id, e.received_at, '\Draft'
  FROM email_messages e
  JOIN folders f ON f.id = e.folder_id
 WHERE e.expunged_at IS NULL
   AND (f.special_use = 'drafts' OR LOWER(f.name) = 'drafts')
ON CONFLICT DO NOTHING;

-- Re-run the reconciliation now those flags exist, so the Sent and Drafts
-- counters land on zero unread rather than on their pre-flag values.
UPDATE folders f
   SET unread_count = c.unread
  FROM (
    SELECT f2.id,
           COUNT(e.id) FILTER (
             WHERE NOT EXISTS (
               SELECT 1 FROM message_flags mf
                WHERE mf.message_id = e.id
                  AND mf.received_at = e.received_at
                  AND mf.flag = '\Seen')
           )::int AS unread
      FROM folders f2
      LEFT JOIN email_messages e
             ON e.folder_id = f2.id AND e.expunged_at IS NULL
     GROUP BY f2.id
  ) AS c
 WHERE f.id = c.id
   AND f.unread_count IS DISTINCT FROM c.unread;

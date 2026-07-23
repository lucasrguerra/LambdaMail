-- Dev-only fixture data for local SMTP testing. Not a schema migration -
-- never applied in production, and safe to re-run (idempotent upserts).
INSERT INTO domains (id, name, punycode_name, is_active)
VALUES ('00000000-0000-0000-0000-000000000001', 'example.test', 'example.test', true)
ON CONFLICT (name) DO NOTHING;

-- Password: "dev-password-only" hashed with Argon2id - never use in production.
INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, is_active)
VALUES (
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000001',
    'postmaster',
    'postmaster@example.test',
    '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG',
    true
)
ON CONFLICT (domain_id, local_part) DO NOTHING;

INSERT INTO folders (id, mailbox_id, name, special_use, parent_id)
VALUES (
    '00000000-0000-0000-0000-000000000003',
    '00000000-0000-0000-0000-000000000002',
    'INBOX',
    'inbox',
    NULL
)
ON CONFLICT (mailbox_id, name) WHERE (parent_id IS NULL) DO NOTHING;

-- Dev-only fixture data for local SMTP testing. Not a schema migration -
-- never applied in production, and safe to re-run (idempotent upserts).
INSERT INTO domains (id, name, punycode_name, is_active)
VALUES ('00000000-0000-0000-0000-000000000001', 'example.test', 'example.test', true)
ON CONFLICT (name) DO NOTHING;

-- Password: "dev-password-only", a real Argon2id PHC hash - never use in
-- production. The value here used to be a hand-written placeholder whose
-- digest was 24 bytes instead of 32, matching no password at all; nothing
-- caught it while the auth service did not verify passwords.
INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, is_active)
VALUES (
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000001',
    'postmaster',
    'postmaster@example.test',
    '$argon2id$v=19$m=65536,t=3,p=4$uPTQ1A3ZH47pN8AIf2h2dQ$rqu1BtCd9yDUtZQ4oUZlK7djiQlY4heqE6DJUn29lRg',
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

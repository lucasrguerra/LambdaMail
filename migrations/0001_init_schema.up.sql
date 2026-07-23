-- 0001_init_schema.up.sql
-- Initial LambdaMail schema. Source of truth: PLAN.md section 9.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;      -- case-insensitive local-part comparison
CREATE EXTENSION IF NOT EXISTS pg_trgm;     -- similarity search

-- =====================================================================
-- 1. DOMAINS
-- =====================================================================
CREATE TABLE domains (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   CITEXT NOT NULL UNIQUE,
    punycode_name          VARCHAR(255) NOT NULL,          -- IDNA for non-ASCII names
    cloudflare_zone_id     VARCHAR(64),
    -- Token is ENCRYPTED (AES-256-GCM), not hashed: it must be reusable.
    cloudflare_token_enc   BYTEA,
    cloudflare_token_nonce BYTEA,
    cloudflare_key_version INT DEFAULT 1,                  -- supports KEK rotation
    dns_status             VARCHAR(16) NOT NULL DEFAULT 'PENDING'
                           CHECK (dns_status IN ('PENDING','VERIFIED','PARTIAL','DRIFT','ERROR')),
    dns_last_checked_at    TIMESTAMPTZ,
    dns_state_hash         VARCHAR(64),
    dmarc_policy           VARCHAR(16) NOT NULL DEFAULT 'quarantine'
                           CHECK (dmarc_policy IN ('none','quarantine','reject')),
    mta_sts_mode           VARCHAR(16) NOT NULL DEFAULT 'testing'
                           CHECK (mta_sts_mode IN ('none','testing','enforce')),
    mta_sts_id             VARCHAR(32),
    dane_enabled           BOOLEAN NOT NULL DEFAULT false,
    max_message_bytes      BIGINT NOT NULL DEFAULT 52428800,   -- 50 MB
    -- Domain MFA policy (see PLAN.md section 14.5). Admins are always required.
    mfa_policy             VARCHAR(16) NOT NULL DEFAULT 'required_admins'
                           CHECK (mfa_policy IN ('optional','required_admins','required_all')),
    is_active              BOOLEAN NOT NULL DEFAULT true,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =====================================================================
-- 2. DKIM KEYS (private key ENCRYPTED + lifecycle for rotation)
-- =====================================================================
CREATE TABLE dkim_keys (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id         UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    selector          VARCHAR(63) NOT NULL,
    algorithm         VARCHAR(16) NOT NULL CHECK (algorithm IN ('rsa2048','rsa4096','ed25519')),
    private_key_enc   BYTEA NOT NULL,
    private_key_nonce BYTEA NOT NULL,
    key_version       INT NOT NULL DEFAULT 1,
    public_key        TEXT NOT NULL,
    status            VARCHAR(16) NOT NULL DEFAULT 'PENDING'
                      CHECK (status IN ('PENDING','ACTIVE','RETIRING','REVOKED')),
    activated_at      TIMESTAMPTZ,
    retire_after      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (domain_id, selector)
);
-- Invariant: at most one ACTIVE key per (domain, algorithm).
CREATE UNIQUE INDEX uq_dkim_active
    ON dkim_keys(domain_id, algorithm) WHERE status = 'ACTIVE';

-- =====================================================================
-- 3. MAILBOXES
-- =====================================================================
CREATE TABLE mailboxes (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id              UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    local_part             CITEXT NOT NULL,
    email_address          CITEXT NOT NULL UNIQUE,
    password_hash          TEXT NOT NULL,                  -- Argon2id, PHC string format
    password_updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    quota_bytes            BIGINT NOT NULL DEFAULT 1073741824,
    used_bytes             BIGINT NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
    max_recipients_per_hour INT NOT NULL DEFAULT 200,      -- contains a compromised account
    failed_login_count     INT NOT NULL DEFAULT 0,
    locked_until           TIMESTAMPTZ,
    -- Interface and notification language for this user (see PLAN.md section 21).
    locale                 VARCHAR(8) NOT NULL DEFAULT 'en'
                           CHECK (locale IN ('en','pt-BR','es')),
    timezone               VARCHAR(64) NOT NULL DEFAULT 'UTC',   -- IANA tz
    role                   VARCHAR(16) NOT NULL DEFAULT 'USER'
                           CHECK (role IN ('USER','DOMAIN_ADMIN','SUPER_ADMIN')),
    is_active              BOOLEAN NOT NULL DEFAULT true,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (domain_id, local_part)
);

-- ---------------------------------------------------------------------
-- 3b. SECOND FACTOR (TOTP RFC 6238 - Google Authenticator, Aegis, 1Password)
-- ---------------------------------------------------------------------
CREATE TABLE mfa_totp (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id     UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    label          VARCHAR(64) NOT NULL DEFAULT 'Authenticator',
    secret_enc     BYTEA NOT NULL,          -- base32 secret, encrypted (AES-256-GCM / KEK)
    secret_nonce   BYTEA NOT NULL,
    key_version    INT NOT NULL DEFAULT 1,
    algorithm      VARCHAR(8)  NOT NULL DEFAULT 'SHA1',   -- SHA1: required by Google Authenticator
    digits         SMALLINT    NOT NULL DEFAULT 6,
    period_seconds SMALLINT    NOT NULL DEFAULT 30,
    -- Two-step enrollment: becomes CONFIRMED only after the user proves one code.
    status         VARCHAR(12) NOT NULL DEFAULT 'PENDING'
                   CHECK (status IN ('PENDING','CONFIRMED','REVOKED')),
    -- Anti-replay (RFC 6238 section 5.2): a code can only be used once.
    last_used_step BIGINT,
    confirmed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX uq_totp_confirmed ON mfa_totp(mailbox_id) WHERE status = 'CONFIRMED';

-- Recovery codes: single use, stored as hash (never recoverable).
CREATE TABLE mfa_recovery_codes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,               -- Argon2id
    used_at    TIMESTAMPTZ,
    used_ip    INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_recovery_unused ON mfa_recovery_codes(mailbox_id) WHERE used_at IS NULL;

-- WebAuthn / passkeys - Phase 6, table created now so MFA doesn't need a breaking migration later.
CREATE TABLE mfa_webauthn_credentials (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id    UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    credential_id BYTEA NOT NULL UNIQUE,
    public_key    BYTEA NOT NULL,
    sign_count    BIGINT NOT NULL DEFAULT 0,
    label         VARCHAR(64),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Trusted devices: "don't ask for the code for 30 days" on this browser.
CREATE TABLE trusted_devices (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id    UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    token_hash    TEXT NOT NULL UNIQUE,
    user_agent    TEXT,
    ip_address    INET,
    surface       VARCHAR(8) NOT NULL CHECK (surface IN ('user','admin')),
    expires_at    TIMESTAMPTZ NOT NULL,
    last_seen_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Webmail/admin sessions, individually revocable by the user.
CREATE TABLE web_sessions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id         UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    surface            VARCHAR(8) NOT NULL CHECK (surface IN ('user','admin')),
    refresh_token_hash TEXT NOT NULL UNIQUE,
    mfa_satisfied      BOOLEAN NOT NULL DEFAULT false,
    mfa_satisfied_at   TIMESTAMPTZ,           -- step-up: admin re-requires after 15 min
    ip_address         INET,
    user_agent         TEXT,
    revoked_at         TIMESTAMPTZ,
    expires_at         TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sessions_active ON web_sessions(mailbox_id) WHERE revoked_at IS NULL;

-- App passwords (IMAP/SMTP clients cannot do 2FA)
CREATE TABLE app_passwords (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id    UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    label         VARCHAR(64) NOT NULL,
    password_hash TEXT NOT NULL,
    scopes        TEXT[] NOT NULL DEFAULT '{imap,pop3,smtp}',
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =====================================================================
-- 4. ALIASES (catch-all and multiple destinations supported)
-- =====================================================================
CREATE TABLE aliases (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id            UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    source_address       CITEXT NOT NULL,                 -- '*@domain' = catch-all
    destination_addresses TEXT[] NOT NULL CHECK (cardinality(destination_addresses) > 0),
    is_catch_all         BOOLEAN NOT NULL DEFAULT false,
    is_active            BOOLEAN NOT NULL DEFAULT true,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (domain_id, source_address)
);

-- =====================================================================
-- 5. FOLDERS (mandatory IMAP state)
-- =====================================================================
CREATE TABLE folders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id      UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,               -- decoded modified UTF-7
    special_use     VARCHAR(16)                          -- RFC 6154
                    CHECK (special_use IN ('inbox','sent','drafts','trash','junk','archive')),
    parent_id       UUID REFERENCES folders(id) ON DELETE CASCADE,
    uid_validity    BIGINT NOT NULL DEFAULT (EXTRACT(EPOCH FROM NOW())::BIGINT),
    uid_next        BIGINT NOT NULL DEFAULT 1,           -- RFC 3501 section 2.3.1.1
    highest_modseq  BIGINT NOT NULL DEFAULT 1,           -- RFC 7162 CONDSTORE
    subscribed      BOOLEAN NOT NULL DEFAULT true,
    unread_count    INT NOT NULL DEFAULT 0,
    total_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- TRAP: UNIQUE(mailbox_id, parent_id, name) does NOT prevent duplicates at the root -
-- in SQL, NULL != NULL, so two root folders named "Work" would both pass.
-- Two partial indexes cover both cases:
CREATE UNIQUE INDEX uq_folder_child ON folders(mailbox_id, parent_id, name)
    WHERE parent_id IS NOT NULL;
CREATE UNIQUE INDEX uq_folder_root  ON folders(mailbox_id, name)
    WHERE parent_id IS NULL;

-- =====================================================================
-- 6. BLOBS (deduplication: 1 email to 50 recipients = 1 file)
-- =====================================================================
CREATE TABLE message_blobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_sha256 CHAR(64) NOT NULL UNIQUE,
    storage_driver VARCHAR(16) NOT NULL CHECK (storage_driver IN ('local','s3')),
    storage_path  VARCHAR(1024) NOT NULL,
    size_bytes    BIGINT NOT NULL,
    ref_count     INT NOT NULL DEFAULT 0 CHECK (ref_count >= 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =====================================================================
-- 7. MESSAGES (partitioned by month - volume grows without bound)
-- =====================================================================
CREATE TABLE email_messages (
    id                 UUID NOT NULL DEFAULT gen_random_uuid(),
    mailbox_id         UUID NOT NULL,
    folder_id          UUID NOT NULL,
    uid                BIGINT NOT NULL,                  -- unique and increasing within the folder
    modseq             BIGINT NOT NULL DEFAULT 1,
    blob_id            UUID NOT NULL REFERENCES message_blobs(id),
    message_id_header  VARCHAR(998),
    in_reply_to        VARCHAR(998),
    thread_id          UUID,                             -- conversation grouping
    sender_address     CITEXT NOT NULL,
    from_display_name  TEXT,
    recipient_addresses TEXT[] NOT NULL,
    subject            TEXT,
    snippet            TEXT,
    size_bytes         BIGINT NOT NULL,
    has_attachments    BOOLEAN NOT NULL DEFAULT false,
    -- Security verdicts
    spf_result         VARCHAR(12) CHECK (spf_result IN ('pass','fail','softfail','neutral','none','temperror','permerror')),
    dkim_result        VARCHAR(12) CHECK (dkim_result IN ('pass','fail','none','policy','temperror','permerror')),
    dmarc_result       VARCHAR(12) CHECK (dmarc_result IN ('pass','fail','none')),
    arc_result         VARCHAR(12),
    dane_status        VARCHAR(12) CHECK (dane_status IN ('verified','skipped','failed','unsupported')),
    tls_version        VARCHAR(8),
    spam_score         NUMERIC(6,2) NOT NULL DEFAULT 0,
    spam_verdict       VARCHAR(12) CHECK (spam_verdict IN ('ham','probable','spam','reject')),
    virus_name         VARCHAR(128),
    received_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expunged_at        TIMESTAMPTZ,                      -- soft delete (IMAP \Deleted + EXPUNGE)
    search_vector      TSVECTOR GENERATED ALWAYS AS (
                          to_tsvector('simple',
                            coalesce(subject,'') || ' ' ||
                            coalesce(snippet,'') || ' ' ||
                            coalesce(sender_address::text,''))
                       ) STORED,
    PRIMARY KEY (id, received_at)
) PARTITION BY RANGE (received_at);

-- Partitions are created by a monthly job (pg_partman or a custom routine).
-- Seed the current month so local dev / F0 has a writable partition out of the box.
CREATE TABLE email_messages_2026_07 PARTITION OF email_messages
    FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

-- Uniqueness index (folder_id, uid): on a partitioned table, every UNIQUE index
-- must include the partition key - so this alone does NOT guarantee the invariant
-- (the same (folder_id, uid) could exist in different partitions). The real guarantee
-- comes from the APPLICATION: UIDs are allocated exclusively via folders.uid_next under
-- SELECT ... FOR UPDATE, in the same transaction as the INSERT. This index is defense
-- in depth within each partition + speeds up FETCH by UID.
CREATE UNIQUE INDEX uq_msg_folder_uid ON email_messages(folder_id, uid, received_at);
CREATE INDEX idx_msg_list      ON email_messages(mailbox_id, folder_id, received_at DESC)
                                 WHERE expunged_at IS NULL;
CREATE INDEX idx_msg_modseq    ON email_messages(folder_id, modseq);   -- CONDSTORE sync
CREATE INDEX idx_msg_search    ON email_messages USING GIN(search_vector);
CREATE INDEX idx_msg_thread    ON email_messages(thread_id) WHERE thread_id IS NOT NULL;
CREATE INDEX idx_msg_msgid     ON email_messages(message_id_header);

-- =====================================================================
-- 8. IMAP FLAGS (separate table: \Seen \Answered \Flagged \Draft + keywords)
-- =====================================================================
CREATE TABLE message_flags (
    message_id  UUID NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    flag        VARCHAR(64) NOT NULL,
    PRIMARY KEY (message_id, received_at, flag),
    -- FK to a partitioned table is supported since PG 12; without it, orphan
    -- flags would accumulate forever after EXPUNGE.
    FOREIGN KEY (message_id, received_at)
        REFERENCES email_messages(id, received_at) ON DELETE CASCADE
);
CREATE INDEX idx_flags_lookup ON message_flags(flag, message_id);

-- =====================================================================
-- 9. OUTBOUND QUEUE (source of truth in Postgres; Redis is only an index)
-- =====================================================================
CREATE TABLE outbound_jobs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id        UUID REFERENCES mailboxes(id) ON DELETE SET NULL,
    blob_id           UUID NOT NULL REFERENCES message_blobs(id),
    envelope_from     CITEXT NOT NULL,                  -- '' = bounce (never re-bounce)
    envelope_to       CITEXT NOT NULL,
    destination_domain CITEXT NOT NULL,
    status            VARCHAR(16) NOT NULL DEFAULT 'QUEUED'
                      CHECK (status IN ('QUEUED','SENDING','DEFERRED','DELIVERED','BOUNCED','CANCELLED')),
    attempt           INT NOT NULL DEFAULT 0,
    next_attempt_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '5 days',
    last_smtp_code    VARCHAR(4),
    last_error        TEXT,
    tls_policy_used   VARCHAR(16),                      -- dane | mta-sts | opportunistic | none
    delay_dsn_sent    BOOLEAN NOT NULL DEFAULT false,
    locked_by         VARCHAR(64),                      -- worker id
    locked_until      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_outbound_ready ON outbound_jobs(next_attempt_at)
    WHERE status IN ('QUEUED','DEFERRED');
CREATE INDEX idx_outbound_domain ON outbound_jobs(destination_domain, status);

-- =====================================================================
-- 10. TRANSACTIONAL OUTBOX (events are never published outside the transaction)
-- =====================================================================
CREATE TABLE domain_events_outbox (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type    VARCHAR(64) NOT NULL,
    aggregate_id  UUID NOT NULL,
    payload       JSONB NOT NULL,
    published_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =====================================================================
-- 11. SIEVE, TLS POLICY CACHE, REPORTS AND AUDIT
-- =====================================================================
CREATE TABLE sieve_scripts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    name       VARCHAR(128) NOT NULL,
    script     TEXT NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (mailbox_id, name)
);
CREATE UNIQUE INDEX uq_sieve_active ON sieve_scripts(mailbox_id) WHERE is_active;

CREATE TABLE tls_policy_cache (
    destination_domain CITEXT PRIMARY KEY,
    policy_type        VARCHAR(16) NOT NULL,   -- dane | mta-sts | none
    policy_body        JSONB NOT NULL,
    mx_patterns        TEXT[],
    fetched_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE dmarc_reports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id    UUID NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    org_name     VARCHAR(255),
    report_id    VARCHAR(255),
    date_begin   TIMESTAMPTZ, date_end TIMESTAMPTZ,
    raw_xml      TEXT,
    summary      JSONB,           -- {pass, fail, sources:[{ip,count,spf,dkim}]}
    UNIQUE (domain_id, org_name, report_id)
);

CREATE TABLE audit_log (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id    UUID, actor_ip INET,
    action      VARCHAR(64) NOT NULL,
    target_type VARCHAR(64), target_id UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_time ON audit_log(created_at DESC);

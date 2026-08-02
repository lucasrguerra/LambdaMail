-- Self-managed ACME state (PLAN.md section 8.3, mode B).
--
-- This lives in Postgres rather than on disk so that recreating the container
-- keeps the account and the issued certificates. Losing them would force a
-- re-registration and a re-issuance on every deploy, which reaches the CA's
-- rate limits quickly.
--
-- Every secret column is encrypted with the same KEK as the DKIM keys: these
-- are reusable secrets, so they are sealed, never hashed.

CREATE TABLE acme_accounts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- One account per directory URL, so staging and production coexist.
    directory_url       VARCHAR(255) NOT NULL UNIQUE,
    email               VARCHAR(255),
    registration_uri    TEXT,
    private_key_enc     BYTEA NOT NULL,
    private_key_nonce   BYTEA NOT NULL,
    key_version         INT NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE acme_certificates (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain              CITEXT NOT NULL UNIQUE,
    certificate_pem     TEXT NOT NULL,
    private_key_enc     BYTEA NOT NULL,
    private_key_nonce   BYTEA NOT NULL,
    key_version         INT NOT NULL DEFAULT 1,
    -- Increments on every key change. A TLSA record published for a
    -- generation must outlive every certificate of that generation
    -- (RFC 7671 section 8.1).
    key_generation      INT NOT NULL DEFAULT 1,
    not_after           TIMESTAMPTZ NOT NULL,
    issued_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_acme_certificates_expiry ON acme_certificates(not_after);

-- The key reserved for the next issuance. It exists ahead of the certificate
-- precisely so its TLSA association can be published and allowed to propagate
-- before the certificate that uses it is served.
CREATE TABLE acme_pending_keys (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain              CITEXT NOT NULL UNIQUE,
    private_key_enc     BYTEA NOT NULL,
    private_key_nonce   BYTEA NOT NULL,
    key_version         INT NOT NULL DEFAULT 1,
    -- SHA-256 of the SubjectPublicKeyInfo, which is the "3 1 1" TLSA value
    -- for this key. Stored so the rollover can publish it without re-deriving.
    tlsa_sha256         CHAR(64) NOT NULL,
    -- When the association was published; the rollover waits out two TTLs
    -- from here before switching the certificate over.
    published_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

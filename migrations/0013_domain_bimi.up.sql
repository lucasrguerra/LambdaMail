-- The BIMI logo a domain publishes, and the certificate that vouches for it.
--
-- One per domain, not per mailbox: BIMI is a brand indicator, and every
-- message from the domain carries the same mark.
--
-- The VMC is a URL rather than a file because it is issued by an external
-- authority and renewed on its own schedule; Gmail and Outlook only render the
-- logo when the record points at one.
CREATE TABLE IF NOT EXISTS domain_bimi (
    domain_id  UUID PRIMARY KEY REFERENCES domains(id) ON DELETE CASCADE,
    svg        BYTEA NOT NULL,
    etag       CHAR(64) NOT NULL,
    vmc_url    TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

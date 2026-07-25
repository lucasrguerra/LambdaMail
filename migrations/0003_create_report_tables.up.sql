-- 0003_create_report_tables.up.sql
-- Tables for DMARC aggregate (RUA) records and TLS-RPT failure reports

CREATE TABLE IF NOT EXISTS dmarc_reports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id    UUID REFERENCES domains(id) ON DELETE CASCADE,
    org_name     VARCHAR(255) NOT NULL,
    report_id    VARCHAR(255) NOT NULL,
    domain       VARCHAR(255) NOT NULL DEFAULT '',
    date_begin   TIMESTAMPTZ,
    date_end     TIMESTAMPTZ,
    raw_xml      TEXT,
    summary      JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE dmarc_reports ALTER COLUMN domain_id DROP NOT NULL;
ALTER TABLE dmarc_reports ADD COLUMN IF NOT EXISTS domain VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE dmarc_reports ADD COLUMN IF NOT EXISTS date_range_begin TIMESTAMPTZ;
ALTER TABLE dmarc_reports ADD COLUMN IF NOT EXISTS date_range_end TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS dmarc_report_records (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id   UUID NOT NULL REFERENCES dmarc_reports(id) ON DELETE CASCADE,
    source_ip   INET NOT NULL,
    count       INT NOT NULL,
    disposition VARCHAR(32) NOT NULL,
    dkim_result VARCHAR(32) NOT NULL,
    spf_result  VARCHAR(32) NOT NULL,
    header_from VARCHAR(255) NOT NULL
);

CREATE TABLE IF NOT EXISTS tls_rpt_reports (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_name VARCHAR(255) NOT NULL,
    report_id         VARCHAR(255) NOT NULL,
    domain            VARCHAR(255) NOT NULL,
    date_range_begin  TIMESTAMPTZ NOT NULL,
    date_range_end    TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tls_rpt_report_policies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id     UUID NOT NULL REFERENCES tls_rpt_reports(id) ON DELETE CASCADE,
    policy_type   VARCHAR(64) NOT NULL,
    success_count INT NOT NULL DEFAULT 0,
    failure_count INT NOT NULL DEFAULT 0
);

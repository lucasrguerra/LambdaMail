-- Make storing a report idempotent, keyed on the reporter and their own id
-- for the report.
--
-- Reports used to be written only by hand through the ingest API, so a
-- duplicate took deliberate effort. They are now parsed automatically out of
-- the mail that carries them, which makes redelivery routine: an SMTP retry
-- after a slow commit, or a reporter that simply sends again, would otherwise
-- store the report a second time and double every number the admin console
-- shows.
--
-- tls_rpt_reports had no unique key at all. dmarc_reports had one over
-- (domain_id, org_name, report_id), but the ingest path never fills domain_id
-- and in Postgres a NULL never equals a NULL - so that key did not prevent a
-- duplicate either.

-- Collapse the duplicates that already accumulated, keeping the earliest row
-- of each group so the child records that point at it stay valid.
DELETE FROM tls_rpt_reports a
 USING tls_rpt_reports b
 WHERE a.organization_name = b.organization_name
   AND a.report_id = b.report_id
   AND (a.created_at, a.id) > (b.created_at, b.id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_tls_rpt_reports_org_report
    ON tls_rpt_reports (organization_name, report_id);

DELETE FROM dmarc_reports a
 USING dmarc_reports b
 WHERE a.org_name = b.org_name
   AND a.report_id = b.report_id
   AND a.domain IS NOT DISTINCT FROM b.domain
   AND a.id > b.id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_dmarc_reports_org_report
    ON dmarc_reports (org_name, report_id);

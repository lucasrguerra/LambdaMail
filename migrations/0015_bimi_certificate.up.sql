-- The BIMI certificate a domain publishes, held here instead of only linked.
--
-- A Verified Mark Certificate is issued by an authority after it has checked
-- the organisation, so it cannot be generated here; but the authority hands
-- back a .pem file, and until now the console only took a URL to one hosted
-- somewhere else. Holding the file lets the service serve it at
-- /.well-known/bimi/vmc.pem and point the DNS record there, next to the logo
-- the certificate was issued for.
--
-- Stored as the PEM chain exactly as delivered. Everything the console shows
-- about it (validity, subject, whether it matches the logo) is read from the
-- file when asked, so it cannot drift from what is actually served.
--
-- vmc_url stays: a domain may keep its certificate with the authority.
ALTER TABLE domain_bimi
    ADD COLUMN IF NOT EXISTS vmc_pem         BYTEA,
    ADD COLUMN IF NOT EXISTS vmc_uploaded_at TIMESTAMPTZ;

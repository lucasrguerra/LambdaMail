# Rspamd with its configuration baked in.
#
# The configuration used to arrive through a bind mount. Rspamd starts happily
# without it, so a mount that resolved to an empty directory would leave the
# rate limits and score thresholds silently unapplied - the defence would look
# configured and do nothing.
#
# Build context is the repository root.
FROM rspamd/rspamd:3.9

COPY config/rspamd /etc/rspamd/local.d

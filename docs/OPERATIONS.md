# Operating LambdaMail

What to watch, what the alerts mean, and how to recover. For the initial
install see [DEPLOYMENT.md](DEPLOYMENT.md).

## Health

`GET /health` on the `protocols` service answers three ways, and the difference
matters:

| Response | Meaning |
| :--- | :--- |
| `200`, `X-LambdaMail-Health: ok` | Healthy |
| `200`, `X-LambdaMail-Health: degraded` | Running, but something needs an operator. The body says what. |
| `503` | A dependency is down — usually Postgres. |

Degraded is deliberately not a failure: restarting cannot fix a certificate the
proxy never issued, and a restart loop would only hide it.

## Certificates

Under `TLS_MODE=traefik` the mail listeners read the certificate Coolify's proxy
manages. Two failure modes are worth understanding.

**No certificate for the mail host.** The proxy only holds certificates for
hosts it routes, so `mail.example.com` needs a router even though nothing is
served over HTTP there. `docker-compose.yaml` declares one; if you removed it,
the listeners fall back to a self-signed certificate and `/health` reports
degraded.

**A certificate that stopped renewing.** The watcher re-reads the store every
60 seconds and reports:

- `WARNING` when the soonest expiry is under 7 days
- `CRITICAL` under 24 hours, or when it has already expired

Watch for these in `docker compose logs protocols`. The admin console's TLS
panel shows the same state, including when the watcher last managed to read the
store — a timestamp that stops advancing means it has gone blind, which is the
failure that shows up 90 days later as an expired certificate.

## Backups

Two things must be backed up, and **they must not live in the same place**:

1. **The database.** `pg_dump` covers mailboxes, messages metadata, DKIM keys
   (encrypted), sessions and the queue.
2. **`LAMBDAMAIL_MASTER_KEY`.** Every DKIM private key, TOTP secret and provider
   token in that dump is encrypted with it. A backup you cannot decrypt is not a
   backup.

Message bodies live in the `mail_storage` volume (or S3, with
`STORAGE_DRIVER=s3`). The database references them by hash; restoring one
without the other leaves rows pointing at blobs that are not there.

```bash
# Database
docker compose exec -T db pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB" | gzip > lambdamail-$(date +%F).sql.gz

# Message bodies
docker run --rm -v lambdamail_mail_storage:/data -v "$PWD:/backup" \
  alpine tar czf /backup/mail-$(date +%F).tar.gz -C /data .
```

Restoring has not been rehearsed in this repository. Before relying on it,
restore into a scratch environment and confirm a message opens — an untested
restore is a hypothesis.

## Accounts

```bash
make create-admin EMAIL=… PASSWORD='…'     # first admin, or --force for another
make reset-password EMAIL=… PASSWORD='…'   # recovers a lockout, ends its sessions
```

An account locks itself for 15 minutes after 5 failed passwords. That is the
lockout working, not a fault; `reset-password` clears it.

If somebody loses their second factor and their recovery codes, there is no
self-service path today. Clear the enrolment directly and have them enrol again:

```sql
DELETE FROM mfa_totp WHERE mailbox_id = (SELECT id FROM mailboxes WHERE email_address = 'them@example.com');
```

## Delivery

The outbound queue lives in Postgres, so it survives a restart of anything. The
retry ladder is 5 min, 15 min, 1 h, then 2/4/8/12 h with jitter, giving up after
5 days with a bounce. A delay notice goes out at the fourth attempt.

```sql
-- What is stuck and why
SELECT destination_domain, status, attempt, last_smtp_code, left(last_error, 80)
  FROM outbound_jobs WHERE status IN ('DEFERRED','BOUNCED')
 ORDER BY created_at DESC LIMIT 20;
```

Reading the failures:

- **`4.7.5` with a TLS message** — the destination published a DANE or MTA-STS
  policy that could not be satisfied. The message is deferred rather than sent
  in the clear, which is the point. If it persists, the destination's
  certificate is broken, not yours.
- **`5.1.1`** — no such recipient. Permanent, bounced immediately.
- **A dial or connection error with no code** — the destination is unreachable;
  it will retry.

The admin console's Queue page shows the same data with retry and cancel.

## DNS drift

With a Cloudflare token, the zone is reconciled on start and every six hours.
It is idempotent, and records it did not create are reported as conflicts
rather than overwritten.

```bash
docker compose logs protocols | grep -E "DNS reconcile|DNS conflict"
```

`PARTIAL` or `DRIFT` in that output means the desired state and the zone
disagree — most often because a record was edited by hand.

## Spam filtering

Rspamd holds its state in Redis and its thresholds in `system_config`, editable
from the admin console. The defaults reject at 15, tag at 6 and greylist at 4.

A message the filter routes to `Junk` is filed there; one over the reject
threshold is refused during the SMTP session, so the sender is told. Nothing is
accepted and silently discarded — a silent discard loses legitimate mail with
nobody aware of it.

## Rotating a DKIM key

From the admin console, or:

```bash
curl -X POST https://<host>/api/v1/admin/dkim/rotate \
  -H 'Content-Type: application/json' -b 'lm_admin_session=…' \
  -d '{"domain":"example.com","selector":"lmail2","algorithm":"rsa2048"}'
```

The previous key is retired with a 7-day overlap rather than deleted: mail
already sent under it is still being verified by receivers who may take a while
to stop resolving the old selector. **Publish the new selector's TXT record
before the overlap ends**, or signatures start failing when the old key goes.

## Rotating the master key

There is no automated path today. It requires decrypting every sealed secret
with the old key and resealing with the new one, and getting it wrong destroys
them. Plan a maintenance window and rehearse against a copy first.

## What is not automated

Being explicit so nothing is assumed:

- Backups are documented, not scheduled. Set up a cron job.
- Restores are not rehearsed.
- There is no metrics endpoint or alerting yet; the signals above are log lines
  and the health endpoint.
- Master key rotation is manual.
- Log retention is whatever Docker's json-file driver is configured for
  (10 MB × 3 per service, in `docker-compose.yaml`).

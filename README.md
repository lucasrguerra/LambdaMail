# LambdaMail

A self-hosted mail server with an integrated webmail and admin console, built to
run on [Coolify](https://coolify.io) from a single `docker-compose.yaml`.

SMTP, IMAP, POP3 and ManageSieve, with SPF, DKIM, DMARC, ARC, MTA-STS and
TLS-RPT, DNS automation through Cloudflare, and spam and virus filtering. No
paid tiers and no artificial limits on domains or mailboxes.

> **Status: pre-release.** The mail path, the security posture and the web
> interfaces work end to end, and there is an automated test suite behind them.
> Several console panels are still read-only, and some features are recorded but
> not yet acted on — see [What is not finished](#what-is-not-finished) before
> putting real mail through it.

---

## What it does

| Area | |
| :--- | :--- |
| **Protocols** | SMTP MX (25), Submission (587 STARTTLS, 465 implicit TLS), IMAP (143/993) with UID, flags, IDLE and CONDSTORE, POP3 (110/995), ManageSieve (4190) |
| **Authentication** | SPF, DKIM (Ed25519 + RSA dual signature), DMARC, ARC sealing, DANE/TLSA and MTA-STS on delivery, TLS-RPT ingestion |
| **Filtering** | Rspamd scoring with greylisting, RBLs and rate limiting; ClamAV; Sieve |
| **Delivery** | Durable outbound queue in Postgres with the retry ladder and RFC 3464 bounces; smarthost fallback for hosts with port 25 blocked |
| **DNS** | Cloudflare reconciliation of the 13 records, idempotent, with drift detection |
| **Web** | Webmail at `/user/*` and an admin console at `/admin/*`, isolated by session audience, with TOTP two-factor, recovery codes, app passwords, and English, Portuguese and Spanish |

## Architecture

Two runtimes, split by what each is good at:

- **`protocols`** (Go) — every protocol listener, the delivery worker, TLS
  certificate handling, DNS reconciliation, and the webmail's mail API. It owns
  folder, UID, flag and blob semantics.
- **`auth`** (TypeScript) — logins, sessions, two-factor, account management and
  the admin console API.
- **`web`** (Next.js) — both web surfaces, and the proxy that gives the browser a
  single origin.
- **`storage`** (TypeScript) — health only today.

Postgres is the source of truth for everything, including the outbound queue.
Redis is a cache and dispatch index; losing it loses no mail.

```
                    ┌──────────┐
  browser ─────────▶│   web    │──┬─▶ auth      (sessions, accounts, admin)
                    └──────────┘  └─▶ protocols (mail read/send, DKIM, TLS)
                                        │
  internet ──25/587/465/143/993──────────┘
                                        │
                              Postgres ─┴─ Redis ─ Rspamd ─ ClamAV
```

## Deploying

For a real VPS, follow **[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)**. It covers the
three things no code can do for you — reverse DNS, the DNSSEC delegation and
getting port 25 unblocked — and the order the rest has to happen in.

The short version:

```bash
cp .env.production.example .env      # then replace every CHANGE
make preflight                       # checks port 25, PTR, RBLs, token scope
docker compose up -d
make create-admin EMAIL=you@example.com PASSWORD='...'
```

`make create-admin` is not optional: a fresh install has no mailbox, and every
route into the console needs one.

### Ports

Everything below is TCP. "Inbound" is what the host's firewall and the
provider's security group must allow; "outbound" is what the host must be able
to reach.

| Port | Direction | Who connects | Required? | Notes |
| ---: | :--- | :--- | :--- | :--- |
| 25 | in | Any sending MTA | **Yes** to receive mail | Must be open to the world. Rate-limited, not authenticated — that is how SMTP works. |
| 25 | **out** | This host → remote MTAs | **Yes** to send mail | The one most providers block. Without it, set a relay (`RELAY_HOST`); see [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md). |
| 80 | in | Let's Encrypt, browsers | **Yes** | ACME HTTP-01 challenge, and the redirect to HTTPS. Closing it means no certificate. |
| 443 | in | Browsers | **Yes** | Webmail, admin console, MTA-STS policy, autodiscover. |
| 587 | in | Mail clients | Recommended | Submission with STARTTLS. The port clients should use. |
| 465 | in | Mail clients | Optional | Submission with implicit TLS, for clients that insist. |
| 143 | in | Mail clients | Optional | IMAP with STARTTLS. Prefer 993. |
| 993 | in | Mail clients | Recommended | IMAPS. |
| 110 / 995 | in | Mail clients | Optional | POP3 / POP3S. Leave closed unless something needs POP3. |
| 4190 | in | Mail clients | Optional | ManageSieve, for editing filters from a desktop client. |
| 53 | out | This host → resolver | **Yes** | MX, SPF, DKIM, DANE and MTA-STS lookups. UDP **and** TCP — DNSSEC answers overflow UDP. |
| 443 | out | This host → APIs | If used | Cloudflare DNS automation, MTA-STS policy fetch, ClamAV signature updates. |

Ports 110, 995 and 4190 are published by the compose file but are worth closing
at the firewall unless a client actually needs them — every open port is one
more thing to keep patched.

### What the host has to allow

- **Reverse DNS (PTR)** for the public IPv4 must resolve to `PRIMARY_MAIL_HOST`,
  and that name must resolve back to the same address. Set by whoever owns the
  IP block — your provider's panel, never Cloudflare. Gmail and Outlook
  spam-folder or refuse mail without it. If the host has a public IPv6 and
  publishes an AAAA record, it needs a PTR too: senders that connect over IPv6
  are judged on the IPv6 reverse.
- **Outbound port 25 unblocked.** Ask the provider; a timeout rather than a
  refusal when connecting to a remote MX is the signature of a block.
- **Docker** with the compose plugin, and a user in the `docker` group or root.
- **Ports below 1024** are bound by Docker's proxy on the host, so the daemon
  needs the privilege — the default on a normal install. The containers
  themselves run unprivileged.
- **The proxy's certificate directory** must be readable by the `protocols`
  container. Under `TLS_MODE=traefik` it reads the certificate the proxy
  obtained, from the directory given by `COOLIFY_PROXY_DIR`, mounted read-only.
  The images run as a non-root user, so an `acme.json` left at mode `600` and
  owned by root cannot be read, and the mail listeners fall back to a
  self-signed certificate while HTTPS keeps working — a confusing failure that
  looks like a certificate problem and is a permissions problem.
- **Disk** for the mail spool (`protocols_spool`) and Postgres. Mail is stored
  once and reference-counted, but plan for growth.
- **A firewall that does not rate-limit port 25 into uselessness.** Some
  providers apply aggressive SYN limits that look like intermittent delivery
  failures.

## Running locally

```bash
cp .env.example .env
make gen-dev-cert                    # self-signed certificate for the listeners
make up                              # builds from source via the build overlay
make seed                            # a postmaster@example.test fixture
```

The webmail is on `http://localhost:3000`. The seeded password is
`dev-password-only` — it is a fixture, not a credential.

## Testing

```bash
make test        # Go suite, auth and webmail
make lint        # go vet, tsc, and the language lint
```

The Go and auth suites include integration tests that need Postgres. They skip
without `TEST_DATABASE_URL` and run against a real database in CI, which is
where the security regressions are enforced — wrong password, wrong TOTP code,
session revocation, surface isolation, cross-mailbox reads.

## Configuration

Every variable is documented inline in
[`.env.production.example`](.env.production.example). The ones without a safe
default:

| Variable | |
| :--- | :--- |
| `JWT_SECRET` | Signs sessions. At least 32 characters; the service refuses to start below that rather than fall back to a constant. |
| `LAMBDAMAIL_MASTER_KEY` | Encrypts DKIM keys, TOTP secrets and provider tokens. **Back it up separately from the database** — losing it makes every stored secret unrecoverable. |
| `POSTGRES_PASSWORD`, `REDIS_PASSWORD` | No defaults; compose fails without them. |
| `MAIL_DOMAIN`, `PRIMARY_MAIL_HOST`, `PUBLIC_IPV4` | Identity. `PRIMARY_MAIL_HOST` must match the PTR of `PUBLIC_IPV4`. |

## Operating

| | |
| :--- | :--- |
| `make preflight` | Port 25 egress, PTR and FCrDNS, RBL listings, Cloudflare token scope, certificate presence |
| `make create-admin EMAIL=… PASSWORD='…'` | First administrator on a fresh install |
| `make reset-password EMAIL=… PASSWORD='…'` | Recovers a locked-out account and signs out its sessions |
| `docker compose logs -f protocols` | Delivery, TLS and DNS reconciliation |
| `GET /health` on `protocols` | `200` healthy, `200` with `X-LambdaMail-Health: degraded` when a certificate needs attention, `503` when a dependency is down |

[docs/OPERATIONS.md](docs/OPERATIONS.md) covers backups, certificate rotation,
what the alerts mean and how to read a delivery failure.

## What is not finished

Stated plainly, because a mail server that half works loses mail:

- **Recorded but not acted on.** Vacation responder, signature and the Sieve
  rules built in the UI are stored, but nothing consumes them yet — enabling a
  vacation reply does not send one.
- **Read-only console panels.** The guided domain onboarding checklist, the DNS
  desired-vs-actual diff and the CSV import report on state rather than driving
  it.
- **Not implemented.** JMAP, BIMI, CalDAV/CardDAV, multi-node. Backups are
  documented but not automated.
- **DANE** is off unless `TLS_MODE=acme`, and deliberately so: under a
  Traefik-managed certificate the key changes at every renewal and the published
  TLSA record stops matching, which permanently rejects mail at every validating
  MTA.

## Contributing

- All code, comments and identifiers are in English. `make lint` enforces it.
- Tests come with the change that needs them; the suites above must stay green.
- Commits reference their issue: `#N: what changed and why`.

## License

MIT — see [LICENSE](LICENSE).

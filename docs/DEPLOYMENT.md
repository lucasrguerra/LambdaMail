# Deploying LambdaMail on Coolify

This is the order things have to happen in. Several steps depend on DNS
propagation or on your provider, so read the whole page before starting — a
couple of them are worth kicking off early.

## Before anything else

Three prerequisites are not automatable, and without them the deployment will
come up but mail will not work. Start them now, because two involve waiting on
somebody else.

### 1. Outbound port 25

Most providers block it by default — AWS, GCP, Azure, Oracle, Vultr, and
DigitalOcean on newer accounts. Open a support ticket asking for outbound SMTP
to be unblocked, and say what the host is for.

If the answer is no, LambdaMail can still send through a relay: set `RELAY_HOST`
and friends, and the relay is added to the published SPF record automatically.
Receiving is unaffected either way.

### 2. Reverse DNS (PTR)

The PTR for your public IPv4 must resolve to `PRIMARY_MAIL_HOST`, and that name
must resolve back to the same address. This is called FCrDNS, and Gmail and
Outlook will refuse or spam-folder mail without it.

**The PTR is set by whoever owns the IP block — your VPS provider, not
Cloudflare.** It is usually a field in the server's control panel.

```bash
dig -x 203.0.113.10 +short        # must print mail.example.com.
dig mail.example.com +short       # must print 203.0.113.10
```

### 3. DNSSEC — only if you want DANE

Signing the zone is a prerequisite for DANE. It is enabled in the Cloudflare
dashboard and needs a DS record published at your registrar, which is manual.

Skip this for a first deployment. DANE stays off under `TLS_MODE=traefik`
anyway, and turning it on without understanding the rollover is the single most
effective way to make every validating MTA reject your mail permanently.

### IPv6

Either configure it properly — AAAA record, matching PTR, and covered by SPF —
or leave `PUBLIC_IPV6` empty. Half-configured IPv6 is a common cause of
rejection, and empty is a safe answer.

---

## Step 1 — Point DNS at the host

Two records must exist before the first deploy, because Coolify's proxy needs
them to obtain certificates:

| Type | Name | Value |
| :--- | :--- | :--- |
| `A` | `mail` | your public IPv4, **DNS only, not proxied** |
| `A`/`CNAME` | `mta-sts` | the same host (may be proxied) |

The `mail` record must not be behind Cloudflare's proxy. SMTP is not HTTP;
proxying it breaks the mail ports and hides your real address from the
certificate challenge.

The rest of the 13 records are created by the service once you give it a
Cloudflare token, or you can add them by hand later.

## Step 2 — Create the application in Coolify

1. **New Resource → Docker Compose**, pointed at this repository.
2. Coolify will read `docker-compose.yaml`. Leave the compose file alone; all
   configuration is environment.
3. Under **Environment Variables**, paste the contents of
   `.env.production.example` and replace every value marked `CHANGE`.

Generate each secret separately:

```bash
openssl rand -base64 36
```

`LAMBDAMAIL_MASTER_KEY` deserves particular care. It encrypts every DKIM
private key, TOTP secret and provider token in the database. **Store a copy
somewhere other than the database backup.** If you lose it, those secrets are
unrecoverable and you will be reissuing DKIM keys and re-enrolling every user's
two-factor.

### Ports

Coolify does not map mail ports for you. In the service settings, make sure the
host publishes:

```
25, 465, 587      SMTP
143, 993          IMAP
110, 995          POP3
4190              ManageSieve
```

These are already declared in `docker-compose.yaml`; what matters is that the
VPS firewall allows them inbound.

### The proxy network

The compose file joins Coolify's proxy network, named `coolify` by default. If
yours differs, set `COOLIFY_PROXY_NETWORK`.

## Step 3 — Deploy and check the preflight

Deploy, then run the diagnostics from the host:

```bash
docker compose run --rm --entrypoint /app/lambdamail-protocols protocols preflight
```

It reports outbound port 25, PTR and FCrDNS, whether your IP is on Spamhaus,
SpamCop or SORBS, the Cloudflare token's scope, and whether a certificate
exists for the mail host. Fix anything it flags before going further —
particularly an RBL listing, which will not fix itself.

## Step 4 — Create the first administrator

Nothing else creates a mailbox, so without this there is no way into the
console:

```bash
make create-admin EMAIL=you@example.com PASSWORD='a long passphrase'
```

This creates the domain if needed, the mailbox as `SUPER_ADMIN`, its standard
folders, and the four system aliases (`postmaster@`, `abuse@`, `dmarc@`,
`tlsrpt@`) that the published DNS records point at.

Then sign in at `https://<WEBMAIL_HOST>/`, which opens the mail client.

An account that may administer the server gets an **Admin console** link in the
webmail sidebar. Following it asks for the password and second factor again,
because the console is a separate audience — being signed in to webmail is
never enough on its own.

If the account has no second factor yet, the admin sign-in enrols one on the
spot: it shows a QR code to scan, takes the first code back, and hands over the
recovery codes. There is no need to visit another screen first, and no way to
end up locked out of the console for want of a factor you could not reach.

Leaving the console needs nothing — the webmail session was never given up.

## Step 5 — DNS records

With a Cloudflare token set, the service reconciles the zone on start and every
six hours after that. Watch it:

```bash
docker compose logs -f protocols | grep "DNS reconcile"
```

It is idempotent, and it never deletes records it did not create — anything
unexpected is reported as a conflict rather than overwritten.

Without a token, add the records by hand. The admin console's Domains page
lists what is expected.

## Step 6 — Verify

Send mail to a Gmail address and check the headers show `spf=pass`,
`dkim=pass`, `dmarc=pass`. Then run a round trip through
[mail-tester.com](https://www.mail-tester.com) and read every point it deducts.

A first deployment will usually be short a few points until the DNS records
have propagated and the IP has some sending history.

---

## Upgrading

Images are published to GHCR for `linux/amd64` and `linux/arm64` on every merge
to `main`.

```bash
docker compose pull
docker compose up -d
```

Deploys pull the images CI published rather than building on the server;
`docker-compose.build.yaml` is the overlay local development uses to build from
source instead.

Migrations run automatically on start, before the services that depend on them.
**Pin `IMAGE_TAG` to a `sha-` tag rather than leaving it on `latest`.** Not
only for rollback: `docker compose up` does not re-pull a tag it already has
locally, so a deployment left on `latest` will happily keep running the image
it pulled the first time while reporting success. An immutable tag changes on
every release, which is what forces the pull.

    IMAGE_TAG=sha-<full commit sha>

The tags are published by `release.yml`; `docker buildx imagetools inspect
ghcr.io/<owner>/lambdamail/webmail:latest` shows what `latest` currently points
at.

**Pinning has a second edge: bump it on every release.** A pinned tag is an
instruction to run *that* image, so a deploy triggered without changing it
re-pulls the same old build and succeeds — the UI simply does not change.
Redeploying with "ignore cache" does not help, because that clears the *build*
cache and nothing here is built on the server; the tag being requested is still
the old one. The two failure modes are mirror images: `latest` never updates
because Docker already has it, and a stale pin never updates because you asked
for it by name.

So a release is two steps, not one:

```bash
# 1. point the tag at the new commit
IMAGE_TAG=sha-$(git rev-parse HEAD)
# 2. then deploy
```

Confirm which one is actually serving before believing a green deployment:

```bash
curl -s https://<WEBMAIL_HOST>/user/login | grep -o '/_next/static/css/[a-z0-9]*\.css'
```

The asset fingerprint changes with every build. If it did not move, the old
image is still running whatever the deployment log said.

## Troubleshooting

**The web UI is up but nothing can log in.** The auth service refuses to start
without a `JWT_SECRET` of at least 32 characters. `docker compose logs auth`
will say so.

**Mail is accepted but never arrives.** Check `docker compose logs -f protocols`
for the delivery worker. A `4.7.5` deferral means the destination published a
TLS policy that could not be satisfied — that is deliberate; the message is
retried rather than sent in the clear.

**`/health` returns `X-LambdaMail-Health: degraded`.** The stack is running but
a certificate needs attention: usually no certificate exists for
`PRIMARY_MAIL_HOST` because no router was declared for it, or one is expiring
within 24 hours. The body says which.

**Everything is fine but Gmail still spam-folders you.** That is reputation, not
configuration. A new IP has none. Send low volumes to engaged recipients first
and watch the DMARC reports in the admin console.

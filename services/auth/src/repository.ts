import { query, queryOne } from "./db.js";
import { verifyPassword, hashPassword, encryptSecret, decryptSecret } from "./crypto.js";
import { verifyTotpCode } from "./totp.js";

// A locked account stays locked for this long once the threshold is reached.
// PLAN.md section 5.2 asks for a temporary block after N failures rather than a
// permanent one, so a wrong-password storm cannot lock a user out for good.
const MAX_FAILED_LOGINS = 5;
const LOCKOUT_MINUTES = 15;

export interface MailboxRecord {
  id: string;
  email_address: string;
  password_hash: string;
  role: "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN";
  domain_id: string;
  locale: string;
  is_active: boolean;
  failed_login_count: number;
  locked_until: Date | null;
  mfa_policy: string;
}

export type LoginOutcome =
  | { status: "OK"; mailbox: MailboxRecord; mfaRequired: boolean }
  | { status: "INVALID_CREDENTIALS" }
  | { status: "LOCKED"; until: Date }
  | { status: "INACTIVE" };

export async function findMailboxByEmail(email: string): Promise<MailboxRecord | null> {
  return queryOne<MailboxRecord>(
    `SELECT m.id, m.email_address, m.password_hash, m.role, m.domain_id, m.locale,
            m.is_active, m.failed_login_count, m.locked_until, d.mfa_policy
       FROM mailboxes m
       JOIN domains d ON d.id = m.domain_id
      WHERE m.email_address = $1`,
    [email],
  );
}

export async function hasConfirmedTotp(mailboxId: string): Promise<boolean> {
  const row = await queryOne<{ count: string }>(
    `SELECT count(*)::text AS count FROM mfa_totp WHERE mailbox_id = $1 AND status = 'CONFIRMED'`,
    [mailboxId],
  );
  return Number(row?.count ?? 0) > 0;
}

/**
 * Decides whether this account must present a second factor now.
 *
 * This is driven by what is actually enrolled, not by policy: a factor that
 * does not exist cannot be demanded, or an account the policy covers could
 * never reach the screen where enrollment happens. Policy decides who *must*
 * enroll (see isMfaEnrollmentRequired) and the admin surface refuses to open
 * at all until they have, which is where PLAN.md section 14.5 is enforced.
 */
export async function isMfaRequired(mailbox: MailboxRecord): Promise<boolean> {
  return hasConfirmedTotp(mailbox.id);
}

/** Whether policy obliges this account to enroll a second factor. */
export function isMfaEnrollmentRequired(mailbox: MailboxRecord): boolean {
  if (mailbox.mfa_policy === "required_all") return true;
  if (mailbox.mfa_policy === "required_admins" && mailbox.role !== "USER") return true;
  return false;
}

/**
 * Authenticates a password against the stored Argon2id hash.
 *
 * The failure counter is advanced on every wrong password and cleared on
 * success, and a locked account is refused before the hash is even computed -
 * which also stops the lockout window from being used as an oracle.
 */
export async function authenticate(email: string, password: string): Promise<LoginOutcome> {
  const mailbox = await findMailboxByEmail(email);

  if (!mailbox) {
    // Hash anyway so that a missing account and a wrong password take
    // comparable time, instead of the response latency revealing which
    // addresses exist.
    await verifyPassword(password, "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
    return { status: "INVALID_CREDENTIALS" };
  }

  if (mailbox.locked_until && mailbox.locked_until.getTime() > Date.now()) {
    return { status: "LOCKED", until: mailbox.locked_until };
  }

  if (!mailbox.is_active) {
    return { status: "INACTIVE" };
  }

  const ok = await verifyPassword(password, mailbox.password_hash);
  if (!ok) {
    await registerFailedLogin(mailbox.id);
    return { status: "INVALID_CREDENTIALS" };
  }

  await query(
    `UPDATE mailboxes SET failed_login_count = 0, locked_until = NULL, updated_at = NOW() WHERE id = $1`,
    [mailbox.id],
  );

  return { status: "OK", mailbox, mfaRequired: await isMfaRequired(mailbox) };
}

async function registerFailedLogin(mailboxId: string): Promise<void> {
  await query(
    `UPDATE mailboxes
        SET failed_login_count = failed_login_count + 1,
            locked_until = CASE WHEN failed_login_count + 1 >= $2
                                THEN NOW() + ($3 || ' minutes')::INTERVAL
                                ELSE locked_until END,
            updated_at = NOW()
      WHERE id = $1`,
    [mailboxId, MAX_FAILED_LOGINS, String(LOCKOUT_MINUTES)],
  );
}

// --------------------------------------------------------------------- TOTP

/**
 * Starts enrollment: the secret is stored PENDING and encrypted at rest, and
 * only the freshly generated one is returned to the caller. It is never
 * accepted back from the client - a secret the client chooses is a secret the
 * client can replay for somebody else's account.
 */
/**
 * Starts - or resumes - a TOTP enrollment, returning the secret to display.
 *
 * Resuming matters. This used to delete any pending row and store a freshly
 * generated secret on every call, and the browser only holds the secret in
 * component state: reloading the settings page, or coming back to it, brought
 * the "enable" button back, and pressing it silently replaced the secret the
 * authenticator had already been given. The app then showed perfectly valid
 * codes for a secret the server no longer had, and every confirmation failed
 * with no indication why.
 *
 * Returning the pending secret instead makes enrollment idempotent: the QR
 * shown after a reload is the same one already scanned, so an existing entry
 * keeps working and rescanning is harmless.
 */
export async function startTotpEnrollment(
  mailboxId: string,
  base32Secret: string,
  label = "Authenticator",
): Promise<string> {
  const pending = await queryOne<{ secret_enc: Buffer; secret_nonce: Buffer; key_version: number }>(
    `SELECT secret_enc, secret_nonce, key_version
       FROM mfa_totp WHERE mailbox_id = $1 AND status = 'PENDING'
       ORDER BY created_at DESC LIMIT 1`,
    [mailboxId],
  );
  if (pending) {
    try {
      return decryptSecret(pending.secret_enc, pending.secret_nonce, pending.key_version);
    } catch {
      // Undecryptable - the master key changed under it. Nobody can ever
      // confirm this row, so replacing it is the only way forward.
      await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1 AND status = 'PENDING'`, [mailboxId]);
    }
  }

  const { encrypted, nonce, keyVersion } = encryptSecret(base32Secret);
  await query(
    `INSERT INTO mfa_totp (mailbox_id, label, secret_enc, secret_nonce, key_version, status)
     VALUES ($1, $2, $3, $4, $5, 'PENDING')`,
    [mailboxId, label, encrypted, nonce, keyVersion],
  );
  return base32Secret;
}

/**
 * Abandons a pending enrollment, so the next one starts from a new secret.
 *
 * Needed because startTotpEnrollment now resumes: someone who deleted the
 * entry from their authenticator would otherwise be handed the same secret
 * they can no longer generate codes for.
 */
export async function resetTotpEnrollment(mailboxId: string): Promise<void> {
  await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1 AND status = 'PENDING'`, [mailboxId]);
}

interface TotpRow {
  id: string;
  secret_enc: Buffer;
  secret_nonce: Buffer;
  key_version: number;
  last_used_step: string | null;
}

async function loadTotp(mailboxId: string, status: "PENDING" | "CONFIRMED"): Promise<TotpRow | null> {
  return queryOne<TotpRow>(
    `SELECT id, secret_enc, secret_nonce, key_version, last_used_step
       FROM mfa_totp WHERE mailbox_id = $1 AND status = $2
       ORDER BY created_at DESC LIMIT 1`,
    [mailboxId, status],
  );
}

/** Confirms enrollment by proving one code against the stored secret. */
/**
 * Checks a code against the pending enrolment without committing anything.
 *
 * Split from the commit so the caller can store the recovery codes first.
 * Confirming and then generating them meant a failure in between - which
 * happened, on memory - left an account with a second factor enabled and no
 * recovery codes, and the person enrolling never saw the codes they are told
 * to save. Leaving the enrolment pending is the recoverable end of that.
 */
export async function verifyPendingTotp(
  mailboxId: string,
  code: string,
): Promise<{ id: string; step: number } | null> {
  const row = await loadTotp(mailboxId, "PENDING");
  if (!row) return null;

  const secret = decryptSecret(row.secret_enc, row.secret_nonce, row.key_version);
  const result = verifyTotpCode(secret, code, row.last_used_step === null ? null : Number(row.last_used_step));
  if (!result.valid) return null;
  return { id: row.id, step: result.step };
}

/** Marks a verified enrolment confirmed. */
export async function markTotpConfirmed(id: string, step: number): Promise<void> {
  await query(
    `UPDATE mfa_totp SET status = 'CONFIRMED', confirmed_at = NOW(), last_used_step = $2 WHERE id = $1`,
    [id, step],
  );
}

/**
 * Verifies a code for an already-enrolled account and burns the step it used,
 * so the same code cannot be replayed inside its own validity window
 * (RFC 6238 section 5.2).
 */
export async function verifyTotpForLogin(mailboxId: string, code: string): Promise<boolean> {
  const row = await loadTotp(mailboxId, "CONFIRMED");
  if (!row) return false;

  const secret = decryptSecret(row.secret_enc, row.secret_nonce, row.key_version);
  const result = verifyTotpCode(secret, code, row.last_used_step === null ? null : Number(row.last_used_step));
  if (!result.valid) return false;

  await query(`UPDATE mfa_totp SET last_used_step = $2 WHERE id = $1`, [row.id, result.step]);
  return true;
}

// ---------------------------------------------------------- recovery codes

export async function storeRecoveryCodes(mailboxId: string, hashes: string[]): Promise<void> {
  await query(`DELETE FROM mfa_recovery_codes WHERE mailbox_id = $1`, [mailboxId]);
  for (const hash of hashes) {
    await query(`INSERT INTO mfa_recovery_codes (mailbox_id, code_hash) VALUES ($1, $2)`, [mailboxId, hash]);
  }
}

/**
 * Consumes a recovery code. Each row has its own Argon2id hash, so every
 * unused code is tried; the matching one is marked used and can never be
 * accepted again.
 */
export async function consumeRecoveryCode(mailboxId: string, code: string, ip: string | null): Promise<boolean> {
  const rows = await query<{ id: string; code_hash: string }>(
    `SELECT id, code_hash FROM mfa_recovery_codes WHERE mailbox_id = $1 AND used_at IS NULL`,
    [mailboxId],
  );

  // Each code carries its own Argon2id hash, so this is linear in the number
  // of unused codes. That is a few seconds at worst on a path used once in a
  // lost-phone emergency, which is a fair trade for not weakening the hash.
  const normalised = code.trim().toUpperCase();
  for (const row of rows) {
    if (await verifyPassword(normalised, row.code_hash)) {
      // Guarded on used_at so two concurrent requests cannot both spend it.
      const updated = await query<{ id: string }>(
        `UPDATE mfa_recovery_codes SET used_at = NOW(), used_ip = $2
          WHERE id = $1 AND used_at IS NULL RETURNING id`,
        [row.id, ip],
      );
      return updated.length === 1;
    }
  }
  return false;
}

export async function countUnusedRecoveryCodes(mailboxId: string): Promise<number> {
  const row = await queryOne<{ count: string }>(
    `SELECT count(*)::text AS count FROM mfa_recovery_codes WHERE mailbox_id = $1 AND used_at IS NULL`,
    [mailboxId],
  );
  return Number(row?.count ?? 0);
}

// ------------------------------------------------------------ app passwords

export async function listAppPasswords(mailboxId: string): Promise<Array<{ id: string; label: string; created_at: Date; last_used_at: Date | null }>> {
  return query(
    `SELECT id, label, created_at, last_used_at FROM app_passwords WHERE mailbox_id = $1 ORDER BY created_at DESC`,
    [mailboxId],
  );
}

export async function createAppPassword(mailboxId: string, label: string, rawPassword: string): Promise<string> {
  const hash = await hashPassword(rawPassword);
  const row = await queryOne<{ id: string }>(
    `INSERT INTO app_passwords (mailbox_id, label, password_hash) VALUES ($1, $2, $3) RETURNING id`,
    [mailboxId, label, hash],
  );
  return row!.id;
}

export async function deleteAppPassword(mailboxId: string, id: string): Promise<boolean> {
  const rows = await query<{ id: string }>(
    `DELETE FROM app_passwords WHERE id = $1 AND mailbox_id = $2 RETURNING id`,
    [id, mailboxId],
  );
  return rows.length === 1;
}

// ---------------------------------------------------------------- sessions

export async function recordSession(
  mailboxId: string,
  surface: "user" | "admin",
  refreshTokenHash: string,
  expiresAt: Date,
  ip: string | null,
  userAgent: string | null,
): Promise<void> {
  await query(
    `INSERT INTO web_sessions (mailbox_id, surface, refresh_token_hash, mfa_satisfied, mfa_satisfied_at, ip_address, user_agent, expires_at)
     VALUES ($1, $2, $3, true, NOW(), $4, $5, $6)`,
    [mailboxId, surface, refreshTokenHash, ip, userAgent, expiresAt],
  );
}

export async function setLocale(mailboxId: string, locale: string): Promise<void> {
  await query(`UPDATE mailboxes SET locale = $2, updated_at = NOW() WHERE id = $1`, [mailboxId, locale]);
}

// ------------------------------------------------------------ admin views
//
// These read the real tables. The previous implementation returned constants,
// which meant the console showed a healthy system no matter what the server
// was actually doing.

export async function dashboardStats(): Promise<Record<string, unknown>> {
  const row = await queryOne<Record<string, string>>(
    `SELECT
       (SELECT count(*) FROM email_messages WHERE received_at > NOW() - INTERVAL '24 hours')::text AS inbound_24h,
       (SELECT count(*) FROM outbound_jobs WHERE created_at > NOW() - INTERVAL '24 hours')::text AS outbound_24h,
       (SELECT count(*) FROM outbound_jobs WHERE status IN ('QUEUED','DEFERRED','SENDING'))::text AS queue_depth,
       (SELECT count(*) FROM outbound_jobs WHERE status = 'BOUNCED'
          AND created_at > NOW() - INTERVAL '24 hours')::text AS bounced_24h,
       (SELECT count(*) FROM domains WHERE dns_status = 'VERIFIED')::text AS domains_verified,
       (SELECT count(*) FROM domains)::text AS domains_total,
       (SELECT count(*) FROM mailboxes WHERE is_active)::text AS mailboxes_active,
       (SELECT COALESCE(sum(used_bytes),0) FROM mailboxes)::text AS storage_used_bytes,
       (SELECT COALESCE(sum(quota_bytes),0) FROM mailboxes)::text AS storage_quota_bytes`,
  );

  const num = (k: string) => Number(row?.[k] ?? 0);
  const outbound = num("outbound_24h");
  return {
    inbound_24h: num("inbound_24h"),
    outbound_24h: outbound,
    queue_depth: num("queue_depth"),
    bounce_rate: outbound > 0 ? Number((num("bounced_24h") / outbound).toFixed(4)) : 0,
    domains_verified: num("domains_verified"),
    domains_total: num("domains_total"),
    mailboxes_active: num("mailboxes_active"),
    storage_used_bytes: num("storage_used_bytes"),
    storage_quota_bytes: num("storage_quota_bytes"),
  };
}

export interface AdminScope {
  role: string;
  domainId: string;
}

/** SUPER_ADMIN sees everything; a DOMAIN_ADMIN is confined to its own domain.
 * PLAN.md section 14.3 requires this at the query level rather than in the UI,
 * so calling the API directly does not widen the view. */
function scopeClause(scope: AdminScope, column: string): { sql: string; params: unknown[] } {
  if (scope.role === "SUPER_ADMIN") return { sql: "TRUE", params: [] };
  return { sql: `${column} = $1`, params: [scope.domainId] };
}

export async function listDomains(scope: AdminScope): Promise<unknown[]> {
  const { sql, params } = scopeClause(scope, "d.id");
  return query(
    `SELECT d.id, d.name, d.dns_status, d.dmarc_policy, d.mta_sts_mode, d.dane_enabled,
            d.is_active, d.dns_last_checked_at,
            (SELECT count(*) FROM mailboxes m WHERE m.domain_id = d.id)::int AS mailbox_count
       FROM domains d WHERE ${sql} ORDER BY d.name`,
    params,
  );
}

export interface UserListFilter {
  search: string;
  active: boolean | null;
  role: string | null;
  offset: number;
  pageSize: number;
}

/**
 * Lists users, filtered and paged in the database.
 *
 * It used to answer with a hardcoded LIMIT 200 and no filter, so an
 * installation with more users than that had no way to reach the rest, and
 * finding one meant scrolling.
 */
export async function listMailboxes(
  scope: AdminScope,
  filter: UserListFilter,
): Promise<{ items: unknown[]; total: number }> {
  const { sql, params } = scopeClause(scope, "m.domain_id");
  const where: string[] = [sql];
  const args: unknown[] = [...params];

  if (filter.search) {
    // Bound as a parameter, never interpolated. The wildcards are added here
    // so the caller searches for a substring without having to know SQL.
    args.push(`%${filter.search}%`);
    where.push(`(m.email_address ILIKE $${args.length} OR d.name ILIKE $${args.length})`);
  }
  if (filter.active !== null) {
    args.push(filter.active);
    where.push(`m.is_active = $${args.length}`);
  }
  if (filter.role) {
    args.push(filter.role);
    where.push(`m.role = $${args.length}`);
  }

  const clause = where.join(" AND ");

  // Counted with the same filter, or the page count would describe a
  // different set of rows than the page itself.
  const totalRow = await queryOne<{ count: string }>(
    `SELECT count(*)::text AS count
       FROM mailboxes m JOIN domains d ON d.id = m.domain_id
      WHERE ${clause}`,
    args,
  );

  args.push(filter.pageSize, filter.offset);
  const items = await query(
    `SELECT m.id, m.email_address, m.role, m.is_active, m.quota_bytes, m.used_bytes,
            m.locale, m.locked_until, d.name AS domain_name, m.domain_id,
            EXISTS (SELECT 1 FROM mfa_totp t WHERE t.mailbox_id = m.id AND t.status = 'CONFIRMED') AS mfa_enrolled
       FROM mailboxes m JOIN domains d ON d.id = m.domain_id
      WHERE ${clause}
      ORDER BY m.email_address
      LIMIT $${args.length - 1} OFFSET $${args.length}`,
    args,
  );

  return { items, total: Number(totalRow?.count ?? 0) };
}

export async function dmarcSummary(): Promise<Record<string, unknown>> {
  const totals = await queryOne<Record<string, string>>(
    `SELECT COALESCE(sum(count),0)::text AS total_messages,
            COALESCE(sum(count) FILTER (WHERE spf_result = 'pass'),0)::text AS spf_pass_count,
            COALESCE(sum(count) FILTER (WHERE dkim_result = 'pass'),0)::text AS dkim_pass_count,
            COALESCE(sum(count) FILTER (WHERE disposition = 'none'),0)::text AS dmarc_pass_count
       FROM dmarc_report_records`,
  );
  const sources = await query(
    `SELECT source_ip::text AS ip, sum(count)::int AS count,
            max(spf_result) AS spf, max(dkim_result) AS dkim
       FROM dmarc_report_records GROUP BY source_ip ORDER BY sum(count) DESC LIMIT 20`,
  );
  return {
    total_messages: Number(totals?.total_messages ?? 0),
    spf_pass_count: Number(totals?.spf_pass_count ?? 0),
    dkim_pass_count: Number(totals?.dkim_pass_count ?? 0),
    dmarc_pass_count: Number(totals?.dmarc_pass_count ?? 0),
    sources,
  };
}

export interface QueueFilter {
  search: string;
  status: string | null;
  offset: number;
  pageSize: number;
}

/**
 * The outbound queue, filtered and paged in the database.
 *
 * It used to return the 50 most recent jobs and nothing else, so a queue that
 * had backed up could not be inspected past its own head - which is precisely
 * when someone needs to look at it.
 */
export async function queueSummary(filter: QueueFilter): Promise<Record<string, unknown>> {
  // The totals are of the whole queue, deliberately: they are the summary the
  // page leads with, and filtering them to the current search would make the
  // counts change as somebody types.
  const byStatus = await query<{ status: string; count: string }>(
    `SELECT status, count(*)::text AS count FROM outbound_jobs GROUP BY status`,
  );

  const where: string[] = ["TRUE"];
  const args: unknown[] = [];

  if (filter.search) {
    args.push(`%${filter.search}%`);
    where.push(
      `(envelope_from ILIKE $${args.length} OR envelope_to ILIKE $${args.length}` +
        ` OR destination_domain ILIKE $${args.length} OR last_error ILIKE $${args.length})`,
    );
  }
  if (filter.status) {
    args.push(filter.status);
    where.push(`status = $${args.length}`);
  }

  const clause = where.join(" AND ");
  const totalRow = await queryOne<{ count: string }>(
    `SELECT count(*)::text AS count FROM outbound_jobs WHERE ${clause}`,
    args,
  );

  args.push(filter.pageSize, filter.offset);
  const recent = await query(
    `SELECT id, envelope_from, envelope_to, destination_domain, status, attempt,
            next_attempt_at, last_smtp_code, last_error, tls_policy_used, created_at
       FROM outbound_jobs WHERE ${clause}
      ORDER BY created_at DESC
      LIMIT $${args.length - 1} OFFSET $${args.length}`,
    args,
  );

  return {
    by_status: Object.fromEntries(byStatus.map((r) => [r.status, Number(r.count)])),
    recent,
    total: Number(totalRow?.count ?? 0),
  };
}

export async function preflightSummary(): Promise<Record<string, unknown>> {
  // The authoritative preflight lives in the Go service, which owns the
  // sockets and the resolver. What the console can report on its own is the
  // persisted state, so it reports that and says where the rest comes from.
  const domains = await query<{ name: string; dns_status: string; dane_enabled: boolean }>(
    `SELECT name, dns_status, dane_enabled FROM domains ORDER BY name`,
  );
  // Each check carries a key and its subject rather than a finished English
  // sentence. The console builds the label from these, so a reader with the
  // interface in Portuguese no longer sees "DNS records for example.com"
  // spliced into an otherwise translated page.
  const checks = domains.map((d) => ({
    key: "dnsRecordsFor",
    target: d.name,
    name: `DNS records for ${d.name}`, // kept for any client not yet using key/target
    status: d.dns_status === "VERIFIED" ? "PASS" : d.dns_status === "PENDING" ? "PENDING" : "FAIL",
    detail: d.dns_status,
  }));
  const failing = checks.filter((c) => c.status === "FAIL").length;
  return {
    status: failing > 0 ? "DEGRADED" : checks.length === 0 ? "UNKNOWN" : "HEALTHY",
    checks,
    note: "Socket-level checks (port 25 egress, PTR, RBL) are reported by the protocols service preflight",
  };
}

// -------------------------------------------------- account self-service

export interface SessionSummary {
  id: string;
  surface: string;
  ip_address: string | null;
  user_agent: string | null;
  created_at: Date;
  expires_at: Date;
  current: boolean;
}

/** Lists the account's live sessions so a user can spot one they do not
 * recognise (PLAN.md section 14.2). */
export async function listSessions(mailboxId: string, currentTokenHash: string): Promise<SessionSummary[]> {
  const rows = await query<Omit<SessionSummary, "current"> & { refresh_token_hash: string }>(
    `SELECT id, surface, ip_address::text AS ip_address, user_agent, created_at, expires_at, refresh_token_hash
       FROM web_sessions
      WHERE mailbox_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
      ORDER BY created_at DESC`,
    [mailboxId],
  );
  return rows.map(({ refresh_token_hash, ...rest }) => ({
    ...rest,
    current: refresh_token_hash === currentTokenHash,
  }));
}

export async function revokeSession(mailboxId: string, sessionId: string): Promise<boolean> {
  const rows = await query<{ id: string }>(
    `UPDATE web_sessions SET revoked_at = NOW()
      WHERE id = $1 AND mailbox_id = $2 AND revoked_at IS NULL RETURNING id`,
    [sessionId, mailboxId],
  );
  return rows.length === 1;
}

/** Revokes every session except the caller's, which is what "sign out
 * everywhere else" has to mean for it to be useful after a password leak. */
export async function revokeOtherSessions(mailboxId: string, currentTokenHash: string): Promise<number> {
  const rows = await query<{ id: string }>(
    `UPDATE web_sessions SET revoked_at = NOW()
      WHERE mailbox_id = $1 AND revoked_at IS NULL AND refresh_token_hash <> $2 RETURNING id`,
    [mailboxId, currentTokenHash],
  );
  return rows.length;
}

export async function isSessionRevoked(tokenHash: string): Promise<boolean> {
  const row = await queryOne<{ revoked: boolean }>(
    `SELECT (revoked_at IS NOT NULL) AS revoked FROM web_sessions WHERE refresh_token_hash = $1`,
    [tokenHash],
  );
  // An unknown hash is treated as live: tokens issued before this table was
  // populated must not all stop working at once.
  return row?.revoked ?? false;
}

/**
 * Changes the password after proving the current one, then revokes every other
 * session: a password change that leaves the attacker's session alive has not
 * actually locked anyone out.
 */
export async function changePassword(
  mailboxId: string,
  currentPassword: string,
  newPassword: string,
  currentTokenHash: string,
): Promise<"OK" | "WRONG_PASSWORD" | "NOT_FOUND"> {
  const row = await queryOne<{ password_hash: string }>(
    `SELECT password_hash FROM mailboxes WHERE id = $1`,
    [mailboxId],
  );
  if (!row) return "NOT_FOUND";
  if (!(await verifyPassword(currentPassword, row.password_hash))) return "WRONG_PASSWORD";

  await query(
    `UPDATE mailboxes SET password_hash = $2, password_updated_at = NOW(), updated_at = NOW() WHERE id = $1`,
    [mailboxId, await hashPassword(newPassword)],
  );
  await revokeOtherSessions(mailboxId, currentTokenHash);
  return "OK";
}

// ------------------------------------------------------------- audit log

/** Records an administrative action. PLAN.md section 14.3 requires every
 * mutation under /admin to leave a trace with actor, IP and what changed. */
export async function recordAudit(
  actorId: string | null,
  actorIp: string | null,
  action: string,
  targetType: string | null,
  targetId: string | null,
  metadata: unknown,
): Promise<void> {
  await query(
    `INSERT INTO audit_log (actor_id, actor_ip, action, target_type, target_id, metadata)
     VALUES ($1, $2, $3, $4, $5, $6)`,
    [actorId, actorIp, action, targetType, targetId, JSON.stringify(metadata ?? {})],
  );
}

export async function listAudit(limit = 100): Promise<unknown[]> {
  return query(
    `SELECT a.id, a.action, a.target_type, a.target_id, a.actor_ip::text AS actor_ip,
            a.metadata, a.created_at, m.email_address AS actor_email
       FROM audit_log a LEFT JOIN mailboxes m ON m.id = a.actor_id
      ORDER BY a.id DESC LIMIT $1`,
    [limit],
  );
}

/** Enables or disables a mailbox, refusing anything outside the caller's scope. */
export async function setMailboxActive(scope: AdminScope, mailboxId: string, isActive: boolean): Promise<boolean> {
  const rows =
    scope.role === "SUPER_ADMIN"
      ? await query<{ id: string }>(
          `UPDATE mailboxes SET is_active = $2, updated_at = NOW() WHERE id = $1 RETURNING id`,
          [mailboxId, isActive],
        )
      : await query<{ id: string }>(
          `UPDATE mailboxes SET is_active = $2, updated_at = NOW()
            WHERE id = $1 AND domain_id = $3 RETURNING id`,
          [mailboxId, isActive, scope.domainId],
        );
  return rows.length === 1;
}

export interface UserUpdate {
  role?: string;
  quotaBytes?: number;
  locale?: string;
  isActive?: boolean;
  unlock?: boolean;
}

/** Roles a user may hold. Anything else is refused rather than stored. */
export const ALLOWED_USER_ROLES = ["USER", "DOMAIN_ADMIN", "SUPER_ADMIN"] as const;

/**
 * Edits a user, refusing anything outside the caller's scope.
 *
 * The console could only disable or delete a user; everything else - the role,
 * the quota, the language, releasing a lockout - meant going to the database
 * by hand.
 */
export async function updateMailbox(
  scope: AdminScope,
  mailboxId: string,
  update: UserUpdate,
): Promise<boolean> {
  const sets: string[] = [];
  const args: unknown[] = [mailboxId];

  if (update.role !== undefined) {
    args.push(update.role);
    sets.push(`role = $${args.length}`);
  }
  if (update.quotaBytes !== undefined) {
    args.push(update.quotaBytes);
    sets.push(`quota_bytes = $${args.length}`);
  }
  if (update.locale !== undefined) {
    args.push(update.locale);
    sets.push(`locale = $${args.length}`);
  }
  if (update.isActive !== undefined) {
    args.push(update.isActive);
    sets.push(`is_active = $${args.length}`);
  }
  if (update.unlock) {
    // Releases a lockout from failed sign-ins without touching the password.
    sets.push(`locked_until = NULL`);
  }

  // Nothing to change is not an error, but it must not become "UPDATE ... SET
  // WHERE", which is a syntax error rather than a no-op.
  if (sets.length === 0) return false;

  sets.push("updated_at = NOW()");

  let where = "id = $1";
  if (scope.role !== "SUPER_ADMIN") {
    args.push(scope.domainId);
    where += ` AND domain_id = $${args.length}`;
  }

  const rows = await query<{ id: string }>(
    `UPDATE mailboxes SET ${sets.join(", ")} WHERE ${where} RETURNING id`,
    args,
  );
  return rows.length === 1;
}

/**
 * Retries or cancels one queued delivery.
 *
 * Retry only moves a job that is not already finished, and clears the next
 * attempt time so the worker picks it up on its next pass rather than waiting
 * out the backoff the operator is trying to skip.
 */
export async function updateQueueJob(scope: AdminScope, jobId: string, action: "retry" | "cancel"): Promise<boolean> {
  const scoped = scope.role === "SUPER_ADMIN" ? "TRUE" : "o.mailbox_id IN (SELECT id FROM mailboxes WHERE domain_id = $2)";
  const params: unknown[] = scope.role === "SUPER_ADMIN" ? [jobId] : [jobId, scope.domainId];

  const sql =
    action === "retry"
      ? `UPDATE outbound_jobs o SET status = 'QUEUED', next_attempt_at = NOW(), last_error = NULL
          WHERE o.id = $1 AND o.status IN ('DEFERRED','BOUNCED') AND ${scoped} RETURNING o.id`
      : `UPDATE outbound_jobs o SET status = 'CANCELLED'
          WHERE o.id = $1 AND o.status IN ('QUEUED','DEFERRED') AND ${scoped} RETURNING o.id`;

  const rows = await query<{ id: string }>(sql, params);
  return rows.length === 1;
}

// ------------------------------------------------- mailbox and alias CRUD

/** Confirms the domain is one the caller may administer, so a DOMAIN_ADMIN
 * cannot create accounts in someone else's domain by passing its id. */
async function domainInScope(scope: AdminScope, domainId: string): Promise<boolean> {
  if (scope.role === "SUPER_ADMIN") {
    const row = await queryOne<{ id: string }>(`SELECT id FROM domains WHERE id = $1`, [domainId]);
    return row !== null;
  }
  return scope.domainId === domainId;
}

export interface CreateMailboxInput {
  domainId: string;
  localPart: string;
  password: string;
  role: "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN";
  quotaBytes: number;
}

export type CreateMailboxResult =
  | { status: "OK"; id: string; email: string }
  | { status: "FORBIDDEN" }
  | { status: "DUPLICATE" }
  | { status: "INVALID" };

/**
 * Creates a mailbox with the standard folder set.
 *
 * The folders are created in the same transaction because delivery needs them:
 * a mailbox without a Junk folder used to make the spam path fail, and one
 * without an INBOX cannot receive at all.
 */
export async function createMailbox(scope: AdminScope, input: CreateMailboxInput): Promise<CreateMailboxResult> {
  const localPart = input.localPart.trim().toLowerCase();
  // RFC 5322 allows more than this, but a conservative local part avoids
  // addresses that later confuse the SMTP and IMAP paths.
  if (!/^[a-z0-9](?:[a-z0-9._%+-]{0,62}[a-z0-9])?$/.test(localPart)) return { status: "INVALID" };
  if (input.password.length < 12) return { status: "INVALID" };
  if (!(await domainInScope(scope, input.domainId))) return { status: "FORBIDDEN" };

  const domain = await queryOne<{ name: string }>(`SELECT name FROM domains WHERE id = $1`, [input.domainId]);
  if (!domain) return { status: "FORBIDDEN" };

  const email = `${localPart}@${domain.name}`;
  const existing = await queryOne<{ id: string }>(`SELECT id FROM mailboxes WHERE email_address = $1`, [email]);
  if (existing) return { status: "DUPLICATE" };

  const hash = await hashPassword(input.password);
  const created = await queryOne<{ id: string }>(
    `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role, quota_bytes)
     VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
    [input.domainId, localPart, email, hash, input.role, input.quotaBytes],
  );

  await query(
    `INSERT INTO folders (mailbox_id, name, special_use)
     SELECT $1, f.name, f.special_use
       FROM (VALUES ('INBOX','inbox'), ('Sent','sent'), ('Drafts','drafts'),
                    ('Trash','trash'), ('Junk','junk'), ('Archive','archive'),
                    -- Where delivered DMARC and TLS-RPT reports are filed.
                    -- IMAP has no special-use role for these, so it is matched
                    -- by name. Without the folder the delivery path falls back
                    -- to INBOX, which is the noise this is here to remove.
                    ('Reports', NULL)) AS f(name, special_use)`,
    [created!.id],
  );

  return { status: "OK", id: created!.id, email };
}

export async function deleteMailbox(scope: AdminScope, mailboxId: string): Promise<boolean> {
  const rows =
    scope.role === "SUPER_ADMIN"
      ? await query<{ id: string }>(`DELETE FROM mailboxes WHERE id = $1 RETURNING id`, [mailboxId])
      : await query<{ id: string }>(
          `DELETE FROM mailboxes WHERE id = $1 AND domain_id = $2 RETURNING id`,
          [mailboxId, scope.domainId],
        );
  return rows.length === 1;
}

export async function listAliases(scope: AdminScope): Promise<unknown[]> {
  const { sql, params } = scopeClause(scope, "a.domain_id");
  return query(
    `SELECT a.id, a.source_address, a.destination_addresses, a.is_catch_all, a.is_active, d.name AS domain_name
       FROM aliases a JOIN domains d ON d.id = a.domain_id
      WHERE ${sql} ORDER BY a.source_address LIMIT 500`,
    params,
  );
}

/**
 * The aliases that deliver to one user.
 *
 * An alias is not owned by a user: it belongs to a domain and carries a list
 * of destinations, which may include addresses on other servers, and a
 * catch-all belongs to the whole domain. What can be shown inside a user is
 * the set of aliases that land in their mailbox, which is what an operator
 * actually wants to know when looking at that person.
 */
export async function listAliasesForMailbox(
  scope: AdminScope,
  mailboxId: string,
): Promise<unknown[] | null> {
  const { sql, params } = scopeClause(scope, "m.domain_id");
  const owner = await queryOne<{ email_address: string; domain_id: string }>(
    `SELECT m.email_address, m.domain_id FROM mailboxes m WHERE m.id = $${params.length + 1} AND ${sql}`,
    [...params, mailboxId],
  );
  // Out of scope and non-existent are answered the same way, so the console
  // of one domain cannot probe for ids belonging to another.
  if (!owner) return null;

  return query(
    `SELECT a.id, a.source_address, a.destination_addresses, a.is_catch_all, a.is_active
       FROM aliases a
      WHERE a.domain_id = $1
        AND EXISTS (
              SELECT 1 FROM unnest(a.destination_addresses) AS dest
               WHERE lower(dest) = lower($2)
            )
      ORDER BY a.source_address`,
    [owner.domain_id, owner.email_address],
  );
}

/** Everything one user's checks need, in a single pass over the database. */
export async function userDiagnosticFacts(
  scope: AdminScope,
  mailboxId: string,
): Promise<Record<string, unknown> | null> {
  const { sql, params } = scopeClause(scope, "m.domain_id");
  const row = await queryOne<Record<string, unknown>>(
    `SELECT m.email_address, m.is_active, m.locked_until,
            m.quota_bytes::text AS quota_bytes, m.used_bytes::text AS used_bytes,
            (m.password_hash IS NOT NULL AND m.password_hash <> '') AS password_hash_present,
            EXISTS (SELECT 1 FROM mfa_totp t WHERE t.mailbox_id = m.id AND t.status = 'CONFIRMED') AS mfa_enrolled,
            (SELECT count(*) FROM aliases a
              WHERE a.domain_id = m.domain_id
                AND EXISTS (SELECT 1 FROM unnest(a.destination_addresses) d
                             WHERE lower(d) = lower(m.email_address)))::int AS alias_count,
            (SELECT length(sc.script) FROM sieve_scripts sc
              WHERE sc.mailbox_id = m.id AND sc.is_active
                AND sc.name <> 'vacation-autoresponder' LIMIT 1) AS sieve_bytes,
            EXISTS (SELECT 1 FROM sieve_scripts sc
                     WHERE sc.mailbox_id = m.id AND sc.is_active
                       AND sc.name = 'vacation-autoresponder') AS vacation_enabled
       FROM mailboxes m
      WHERE m.id = $${params.length + 1} AND ${sql}`,
    [...params, mailboxId],
  );
  return row ?? null;
}

/** Everything the server-wide checks need. */
export async function serverDiagnosticFacts(scope: AdminScope): Promise<Record<string, unknown>> {
  const { sql, params } = scopeClause(scope, "d.id");
  const domains = await query<{ name: string; dns_status: string; dane_enabled: boolean }>(
    `SELECT d.name, d.dns_status, d.dane_enabled FROM domains d WHERE ${sql} ORDER BY d.name`,
    params,
  );

  const withoutDkim = await query<{ name: string }>(
    `SELECT d.name FROM domains d
      WHERE ${sql}
        AND NOT EXISTS (SELECT 1 FROM dkim_keys k WHERE k.domain_id = d.id)
      ORDER BY d.name`,
    params,
  );

  const queue = await query<{ status: string; count: string }>(
    `SELECT status, count(*)::text AS count FROM outbound_jobs GROUP BY status`,
  );

  const { sql: mSql, params: mParams } = scopeClause(scope, "m.domain_id");
  const quota = await queryOne<{ over: string; total: string }>(
    `SELECT count(*) FILTER (WHERE m.quota_bytes > 0 AND m.used_bytes >= m.quota_bytes)::text AS over,
            count(*)::text AS total
       FROM mailboxes m WHERE ${mSql}`,
    mParams,
  );

  return {
    domains: domains.map((d) => ({
      name: d.name,
      dnsStatus: d.dns_status,
      daneEnabled: d.dane_enabled,
    })),
    domainsWithoutDkim: withoutDkim.map((d) => d.name),
    queueByStatus: Object.fromEntries(queue.map((r) => [r.status, Number(r.count)])),
    usersOverQuota: Number(quota?.over ?? 0),
    usersTotal: Number(quota?.total ?? 0),
  };
}

export type AliasResult = { status: "OK"; id: string } | { status: "FORBIDDEN" } | { status: "DUPLICATE" } | { status: "INVALID" };

export async function createAlias(
  scope: AdminScope, domainId: string, source: string, destinations: string[],
): Promise<AliasResult> {
  const clean = destinations.map((d) => d.trim().toLowerCase()).filter(Boolean);
  if (clean.length === 0 || !source.trim()) return { status: "INVALID" };
  if (!(await domainInScope(scope, domainId))) return { status: "FORBIDDEN" };

  const existing = await queryOne<{ id: string }>(
    `SELECT id FROM aliases WHERE domain_id = $1 AND source_address = $2`, [domainId, source.trim().toLowerCase()]);
  if (existing) return { status: "DUPLICATE" };

  const row = await queryOne<{ id: string }>(
    `INSERT INTO aliases (domain_id, source_address, destination_addresses, is_catch_all)
     VALUES ($1, $2, $3, $4) RETURNING id`,
    [domainId, source.trim().toLowerCase(), clean, source.trim().startsWith("*@")],
  );
  return { status: "OK", id: row!.id };
}

export async function deleteAlias(scope: AdminScope, aliasId: string): Promise<boolean> {
  // System aliases (postmaster, abuse, dmarc, tlsrpt) are referenced by the
  // published DNS records, so removing them silently breaks reporting.
  const rows =
    scope.role === "SUPER_ADMIN"
      ? await query<{ id: string }>(
          `DELETE FROM aliases WHERE id = $1 AND NOT COALESCE(is_system, false) RETURNING id`, [aliasId])
      : await query<{ id: string }>(
          `DELETE FROM aliases WHERE id = $1 AND domain_id = $2 AND NOT COALESCE(is_system, false) RETURNING id`,
          [aliasId, scope.domainId]);
  return rows.length === 1;
}

// ------------------------------------------------ user preferences & sieve

export async function getUserPreferences(mailboxId: string): Promise<{ signature: string; auto_save_drafts: boolean }> {
  const row = await queryOne<{ signature: string | null; auto_save_drafts: boolean | null }>(
    `SELECT signature, auto_save_drafts FROM mailboxes WHERE id = $1`,
    [mailboxId],
  );
  return {
    signature: row?.signature ?? "",
    auto_save_drafts: row?.auto_save_drafts ?? true,
  };
}

export async function updateUserPreferences(mailboxId: string, signature: string, autoSaveDrafts: boolean): Promise<boolean> {
  const rows = await query<{ id: string }>(
    `UPDATE mailboxes SET signature = $2, auto_save_drafts = $3, updated_at = NOW() WHERE id = $1 RETURNING id`,
    [mailboxId, signature, autoSaveDrafts],
  );
  return rows.length === 1;
}

export async function getSieveScript(mailboxId: string): Promise<{ name: string; script: string; is_active: boolean } | null> {
  return queryOne<{ name: string; script: string; is_active: boolean }>(
    `SELECT name, script, is_active FROM sieve_scripts WHERE mailbox_id = $1 ORDER BY is_active DESC LIMIT 1`,
    [mailboxId],
  );
}

export async function saveSieveScript(mailboxId: string, name: string, script: string, isActive: boolean): Promise<void> {
  if (isActive) {
    await query(`UPDATE sieve_scripts SET is_active = false WHERE mailbox_id = $1`, [mailboxId]);
  }
  await query(
    `INSERT INTO sieve_scripts (mailbox_id, name, script, is_active)
     VALUES ($1, $2, $3, $4)
     ON CONFLICT (mailbox_id, name) DO UPDATE SET script = EXCLUDED.script, is_active = EXCLUDED.is_active`,
    [mailboxId, name, script, isActive],
  );
}

// --------------------------------------------- admin extended features

export async function bulkImportMailboxes(
  scope: AdminScope,
  rows: Array<{ email: string; role: "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN"; quota_mb: number; password?: string }>,
): Promise<{ imported: number; failed: number; errors: string[] }> {
  let imported = 0;
  let failed = 0;
  const errors: string[] = [];

  for (const item of rows) {
    const parts = item.email.trim().toLowerCase().split("@");
    if (parts.length !== 2) {
      failed++;
      errors.push(`Invalid email format: ${item.email}`);
      continue;
    }
    const [localPart, domainName] = parts;
    const domain = await queryOne<{ id: string }>(`SELECT id FROM domains WHERE name = $1`, [domainName]);
    if (!domain) {
      failed++;
      errors.push(`Domain not found: ${domainName}`);
      continue;
    }

    const res = await createMailbox(scope, {
      domainId: domain.id,
      localPart,
      password: item.password || "TempPassword123!",
      role: item.role || "USER",
      quotaBytes: (item.quota_mb || 1024) * 1024 * 1024,
    });

    if (res.status === "OK") {
      imported++;
    } else {
      failed++;
      errors.push(`Failed to import ${item.email}: ${res.status}`);
    }
  }

  return { imported, failed, errors };
}

/**
 * Creates a domain and the aliases its published DNS records point at.
 *
 * Only a SUPER_ADMIN may do this: a DOMAIN_ADMIN is scoped to the domain it
 * already administers, and creating new ones is how that scope would be
 * escaped. The previous version ignored the scope argument entirely.
 */
export async function onboardDomain(
  scope: AdminScope,
  name: string,
  adminAddress: string,
): Promise<{ id: string; name: string; aliases: string[] } | null> {
  if (scope.role !== "SUPER_ADMIN") return null;

  const cleanName = name.trim().toLowerCase();
  // A domain name goes into DNS and into every address on it, so it is
  // validated rather than merely checked for a dot.
  if (!/^(?=.{1,253}$)(?!-)[a-z0-9-]{1,63}(?<!-)(\.(?!-)[a-z0-9-]{1,63}(?<!-))+$/.test(cleanName)) {
    return null;
  }

  const existing = await queryOne<{ id: string }>(`SELECT id FROM domains WHERE name = $1`, [cleanName]);
  let domainId = existing?.id;

  if (!domainId) {
    // name is CITEXT and punycode_name is VARCHAR, so the value is bound
    // twice: one placeholder for both makes Postgres refuse to deduce a type.
    const row = await queryOne<{ id: string }>(
      `INSERT INTO domains (name, punycode_name, dns_status, dmarc_policy, mta_sts_mode)
       VALUES ($1, $2, 'PENDING', 'quarantine', 'testing') RETURNING id`,
      [cleanName, cleanName],
    );
    domainId = row!.id;
  }

  // PLAN.md section 7.4b: these four exist because the published records name
  // them. Publishing rua=mailto:dmarc@ without the alias makes the reports
  // bounce, and receivers then stop sending them.
  const created: string[] = [];
  for (const local of ["postmaster", "abuse", "dmarc", "tlsrpt"]) {
    const source = `${local}@${cleanName}`;
    // The destination is a bound parameter, not interpolated. Building the
    // array in the SQL text let a domain name containing a quote inject.
    await query(
      `INSERT INTO aliases (domain_id, source_address, destination_addresses, is_system)
       VALUES ($1, $2, $3, true)
       ON CONFLICT (domain_id, source_address) DO NOTHING`,
      [domainId, source, [adminAddress]],
    );
    created.push(source);
  }

  return { id: domainId, name: cleanName, aliases: created };
}

export async function getRspamdThresholds(): Promise<{ greylist: number; add_header: number; reject: number }> {
  const row = await queryOne<{ value: { greylist: number; add_header: number; reject: number } }>(
    `SELECT value FROM system_config WHERE key = 'rspamd_thresholds'`,
  );
  return row?.value ?? { greylist: 4.0, add_header: 6.0, reject: 15.0 };
}

export async function updateRspamdThresholds(
  greylist: number,
  addHeader: number,
  reject: number,
): Promise<void> {
  const payload = { greylist, add_header: addHeader, reject };
  await query(
    `INSERT INTO system_config (key, value, updated_at) VALUES ('rspamd_thresholds', $1, NOW())
     ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
    [JSON.stringify(payload)],
  );
}

export async function traceLogsByQueueId(queueId: string): Promise<unknown[]> {
  if (!queueId.trim()) return [];
  const term = `%${queueId.trim()}%`;
  return query(
    `SELECT a.id, a.action, a.target_type, a.target_id, a.actor_ip::text AS actor_ip,
            a.metadata, a.created_at
       FROM audit_log a
      WHERE a.metadata::text LIKE $1 OR a.target_id::text LIKE $1
      ORDER BY a.id DESC LIMIT 50`,
    [term],
  );
}


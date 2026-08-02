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
export async function startTotpEnrollment(mailboxId: string, base32Secret: string, label = "Authenticator"): Promise<void> {
  const { encrypted, nonce, keyVersion } = encryptSecret(base32Secret);
  // Any earlier unconfirmed attempt is replaced, so a user who restarts
  // enrollment is not left with rows nobody can complete.
  await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1 AND status = 'PENDING'`, [mailboxId]);
  await query(
    `INSERT INTO mfa_totp (mailbox_id, label, secret_enc, secret_nonce, key_version, status)
     VALUES ($1, $2, $3, $4, $5, 'PENDING')`,
    [mailboxId, label, encrypted, nonce, keyVersion],
  );
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
export async function confirmTotpEnrollment(mailboxId: string, code: string): Promise<boolean> {
  const row = await loadTotp(mailboxId, "PENDING");
  if (!row) return false;

  const secret = decryptSecret(row.secret_enc, row.secret_nonce, row.key_version);
  const result = verifyTotpCode(secret, code, row.last_used_step === null ? null : Number(row.last_used_step));
  if (!result.valid) return false;

  await query(
    `UPDATE mfa_totp SET status = 'CONFIRMED', confirmed_at = NOW(), last_used_step = $2 WHERE id = $1`,
    [row.id, result.step],
  );
  return true;
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

export async function listMailboxes(scope: AdminScope): Promise<unknown[]> {
  const { sql, params } = scopeClause(scope, "m.domain_id");
  return query(
    `SELECT m.id, m.email_address, m.role, m.is_active, m.quota_bytes, m.used_bytes,
            m.locale, m.locked_until, d.name AS domain_name,
            EXISTS (SELECT 1 FROM mfa_totp t WHERE t.mailbox_id = m.id AND t.status = 'CONFIRMED') AS mfa_enrolled
       FROM mailboxes m JOIN domains d ON d.id = m.domain_id
      WHERE ${sql} ORDER BY m.email_address LIMIT 200`,
    params,
  );
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

export async function queueSummary(): Promise<Record<string, unknown>> {
  const byStatus = await query<{ status: string; count: string }>(
    `SELECT status, count(*)::text AS count FROM outbound_jobs GROUP BY status`,
  );
  const recent = await query(
    `SELECT id, envelope_from, envelope_to, destination_domain, status, attempt,
            next_attempt_at, last_smtp_code, last_error, tls_policy_used
       FROM outbound_jobs ORDER BY created_at DESC LIMIT 50`,
  );
  return {
    by_status: Object.fromEntries(byStatus.map((r) => [r.status, Number(r.count)])),
    recent,
  };
}

export async function preflightSummary(): Promise<Record<string, unknown>> {
  // The authoritative preflight lives in the Go service, which owns the
  // sockets and the resolver. What the console can report on its own is the
  // persisted state, so it reports that and says where the rest comes from.
  const domains = await query<{ name: string; dns_status: string; dane_enabled: boolean }>(
    `SELECT name, dns_status, dane_enabled FROM domains ORDER BY name`,
  );
  const checks = domains.map((d) => ({
    name: `DNS records for ${d.name}`,
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

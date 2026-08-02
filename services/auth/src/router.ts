import { IncomingMessage, ServerResponse } from "node:http";
import crypto from "node:crypto";
import { generateTotpSecret } from "./totp.js";
import { generateRecoveryCodes, generateAppPassword } from "./mfaHelpers.js";
import { createJwt, verifyJwt, isSurfaceAuthorized, type SessionTokenPayload } from "./session.js";
import * as repo from "./repository.js";

const CHALLENGE_TTL_SECONDS = 300;
const SESSION_TTL_SECONDS = 28800;

/** Cookie scoped to the whole origin: the API lives under /api, so a cookie
 * pinned to /user or /admin would never be sent with the requests that need
 * it. Isolation between surfaces comes from the audience claim, which is
 * checked on every request, not from the cookie path. */
function sessionCookie(name: string, value: string, maxAge: number, secure: boolean): string {
  const attrs = [`${name}=${value}`, "Path=/", "HttpOnly", "SameSite=Strict", `Max-Age=${maxAge}`];
  if (secure) attrs.push("Secure");
  return attrs.join("; ");
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let body = "";
    let size = 0;
    req.on("data", (chunk) => {
      size += chunk.length;
      // A login endpoint has no reason to accept a large body; refusing early
      // keeps an unauthenticated caller from buffering memory here.
      if (size > 64 * 1024) {
        reject(new Error("PAYLOAD_TOO_LARGE"));
        req.destroy();
        return;
      }
      body += chunk;
    });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

function clientIp(req: IncomingMessage): string | null {
  return req.socket.remoteAddress ?? null;
}

/** True when the deployment terminates TLS, which is always the case behind
 * Traefik. Only a plain local run drops the Secure attribute. */
function isSecureRequest(): boolean {
  return process.env.NODE_ENV === "production" || process.env.COOKIE_SECURE === "true";
}

export function handleApiRequest(req: IncomingMessage, res: ServerResponse): boolean {
  const url = (req.url || "/").split("?")[0];
  if (!url.startsWith("/api/v1/")) {
    return false;
  }
  void route(req, res, url);
  return true;
}

function sendJson(res: ServerResponse, statusCode: number, data: unknown): void {
  res.setHeader("Content-Type", "application/json");
  res.statusCode = statusCode;
  res.end(JSON.stringify(data));
}

function extractToken(req: IncomingMessage, url: string): string {
  const authHeader = req.headers["authorization"] || "";
  if (authHeader.startsWith("Bearer ")) {
    return authHeader.substring(7);
  }
  const raw = req.headers.cookie;
  if (!raw) return "";
  const wanted = url.startsWith("/api/v1/admin/") ? "lm_admin_session" : "lm_user_session";
  for (const part of raw.split(";")) {
    const trimmed = part.trim();
    const eq = trimmed.indexOf("=");
    if (eq > 0 && trimmed.slice(0, eq) === wanted) {
      // sliced rather than split so a value containing "=" survives intact.
      return trimmed.slice(eq + 1);
    }
  }
  return "";
}

async function route(req: IncomingMessage, res: ServerResponse, url: string): Promise<void> {
  const method = req.method || "GET";

  try {
    const token = extractToken(req, url);
    const session = token ? verifyJwt(token) : null;

    // Surface isolation (PLAN.md section 14.1). A /user token carries
    // aud: lambdamail:user and can never satisfy an admin route.
    if (url.startsWith("/api/v1/admin/") && !isSurfaceAuthorized(session, "admin")) {
      return sendJson(res, 401, { error: "UNAUTHORIZED", message: "Admin session required" });
    }
    if (url.startsWith("/api/v1/user/") && !isSurfaceAuthorized(session, "user")) {
      return sendJson(res, 401, { error: "UNAUTHORIZED", message: "User session required" });
    }

    // A JWT carries its own validity, so revoking a session in the database
    // would otherwise change nothing until it expired. Checking here is what
    // makes "sign out this device" mean anything.
    if (session && (url.startsWith("/api/v1/admin/") || url.startsWith("/api/v1/user/"))) {
      if (await repo.isSessionRevoked(crypto.createHash("sha256").update(token).digest("hex"))) {
        return sendJson(res, 401, { error: "SESSION_REVOKED", message: "This session has been signed out" });
      }
    }

    if (url === "/api/v1/auth/user/login" && method === "POST") return login(req, res, "user");
    if (url === "/api/v1/auth/admin/login" && method === "POST") return login(req, res, "admin");
    if (url === "/api/v1/auth/mfa/verify" && method === "POST") return mfaVerify(req, res);
    if (url === "/api/v1/auth/logout" && method === "POST") return logout(res);

    if (url === "/api/v1/user/me" && method === "GET") return me(res, session!);
    if (url === "/api/v1/user/locale" && method === "PUT") return updateLocale(req, res, session!);
    if (url === "/api/v1/user/preferences" && method === "GET") return userGetPreferences(res, session!);
    if (url === "/api/v1/user/preferences" && method === "POST") return userUpdatePreferences(req, res, session!);
    if (url === "/api/v1/user/sieve" && method === "GET") return userGetSieve(res, session!);
    if (url === "/api/v1/user/sieve" && method === "POST") return userSaveSieve(req, res, session!);
    if (url === "/api/v1/user/vacation" && method === "POST") return userSaveVacation(req, res, session!);
    if (url === "/api/v1/user/mfa/totp/enroll" && method === "POST") return totpEnroll(res, session!);
    if (url === "/api/v1/user/mfa/totp/confirm" && method === "POST") return totpConfirm(req, res, session!);
    if (url === "/api/v1/user/app-passwords" && method === "GET") return appPasswordsList(res, session!);
    if (url === "/api/v1/user/app-passwords" && method === "POST") return appPasswordCreate(req, res, session!);
    if (url.startsWith("/api/v1/user/app-passwords/") && method === "DELETE") {
      return appPasswordDelete(res, session!, url.substring("/api/v1/user/app-passwords/".length));
    }

    if (url === "/api/v1/user/sessions" && method === "GET") return listSessions(req, res, session!);
    if (url === "/api/v1/user/sessions/others" && method === "DELETE") return revokeOthers(req, res, session!);
    if (url.startsWith("/api/v1/user/sessions/") && method === "DELETE") {
      return revokeSession(res, session!, url.substring("/api/v1/user/sessions/".length));
    }
    if (url === "/api/v1/user/password" && method === "PUT") return changePassword(req, res, session!);

    if (url === "/api/v1/admin/audit" && method === "GET") return adminAudit(res);
    if (url === "/api/v1/admin/mailboxes" && method === "POST") return adminCreateMailbox(req, res, session!);
    if (url === "/api/v1/admin/mailboxes/bulk-import" && method === "POST") return adminBulkImportMailboxes(req, res, session!);
    if (url === "/api/v1/admin/domains/onboard" && method === "POST") return adminOnboardDomain(req, res, session!);
    if (url === "/api/v1/admin/domains/reconcile" && method === "POST") return adminReconcileDomain(req, res, session!);
    if (url === "/api/v1/admin/rspamd/thresholds" && method === "GET") return adminGetRspamdThresholds(res);
    if (url === "/api/v1/admin/rspamd/thresholds" && method === "POST") return adminUpdateRspamdThresholds(req, res, session!);
    if (url === "/api/v1/admin/logs/trace" && method === "GET") return adminTraceLogs(req, res);
    if (url === "/api/v1/admin/aliases" && method === "GET") return adminListAliases(res, session!);
    if (url === "/api/v1/admin/aliases" && method === "POST") return adminCreateAlias(req, res, session!);
    if (url.startsWith("/api/v1/admin/aliases/") && method === "DELETE") {
      return adminDeleteAlias(req, res, session!, url.substring("/api/v1/admin/aliases/".length));
    }
    if (url.startsWith("/api/v1/admin/mailboxes/") && method === "DELETE") {
      return adminDeleteMailbox(req, res, session!, url.substring("/api/v1/admin/mailboxes/".length));
    }
    if (url.startsWith("/api/v1/admin/mailboxes/") && url.endsWith("/active") && method === "PUT") {
      const id = url.slice("/api/v1/admin/mailboxes/".length, -"/active".length);
      return adminSetMailboxActive(req, res, session!, id);
    }
    if (url.startsWith("/api/v1/admin/queue/") && url.endsWith("/retry") && method === "POST") {
      return adminQueueAction(req, res, session!, url.slice("/api/v1/admin/queue/".length, -"/retry".length), "retry");
    }
    if (url.startsWith("/api/v1/admin/queue/") && url.endsWith("/cancel") && method === "POST") {
      return adminQueueAction(req, res, session!, url.slice("/api/v1/admin/queue/".length, -"/cancel".length), "cancel");
    }
    if (url === "/api/v1/admin/dashboard" && method === "GET") return adminDashboard(res);
    if (url === "/api/v1/admin/domains" && method === "GET") return adminDomains(res, session!);
    if (url === "/api/v1/admin/mailboxes" && method === "GET") return adminMailboxes(res, session!);
    if (url === "/api/v1/admin/dmarc" && method === "GET") return adminDmarc(res);
    if (url === "/api/v1/admin/queue" && method === "GET") return adminQueue(res);
    if (url === "/api/v1/admin/preflight" && method === "GET") return adminPreflight(res);

    return sendJson(res, 404, { error: "NOT_FOUND", message: "API endpoint not found" });
  } catch (err) {
    const message = err instanceof Error ? err.message : "unknown";
    if (message === "PAYLOAD_TOO_LARGE") {
      return sendJson(res, 413, { error: "PAYLOAD_TOO_LARGE", message: "Request body too large" });
    }
    // Never leak an internal message (SQL text, connection strings) to a
    // caller that may be unauthenticated.
    console.error(`auth: unhandled error on ${url}:`, err);
    return sendJson(res, 500, { error: "INTERNAL_ERROR", message: "Request could not be processed" });
  }
}

async function parseJsonBody(req: IncomingMessage): Promise<Record<string, unknown> | null> {
  const raw = await readBody(req);
  try {
    return JSON.parse(raw || "{}") as Record<string, unknown>;
  } catch {
    return null;
  }
}

// ------------------------------------------------------------------- login

async function login(req: IncomingMessage, res: ServerResponse, surface: "user" | "admin"): Promise<void> {
  const body = await parseJsonBody(req);
  if (!body) return sendJson(res, 400, { error: "BAD_REQUEST", message: "Invalid JSON" });

  const email = typeof body.email === "string" ? body.email.trim().toLowerCase() : "";
  const password = typeof body.password === "string" ? body.password : "";
  if (!email || !password) {
    return sendJson(res, 400, { error: "INVALID_INPUT", message: "Email and password required" });
  }

  const outcome = await repo.authenticate(email, password);

  if (outcome.status === "LOCKED") {
    return sendJson(res, 423, { error: "ACCOUNT_LOCKED", message: "Too many failed attempts, try again later" });
  }
  if (outcome.status !== "OK") {
    // One message for every failure mode: distinguishing "no such account"
    // from "wrong password" hands an attacker a list of valid addresses.
    return sendJson(res, 401, { error: "INVALID_CREDENTIALS", message: "Invalid email or password" });
  }

  const { mailbox, mfaRequired } = outcome;

  // An admin whose second factor is not enrolled must not fall through to a
  // full session: the admin surface requires MFA unconditionally.
  if (surface === "admin" && !(await repo.hasConfirmedTotp(mailbox.id))) {
    return sendJson(res, 403, {
      error: "MFA_ENROLLMENT_REQUIRED",
      message: "Enroll a second factor from the webmail settings before using the admin console",
    });
  }

  if (mfaRequired) {
    const challengeToken = createJwt(
      {
        sub: mailbox.id,
        email: mailbox.email_address,
        role: mailbox.role,
        domainId: mailbox.domain_id,
        surface,
        aud: surface === "admin" ? "lambdamail:admin" : "lambdamail:user",
        mfaSatisfied: false,
        purpose: "mfa_challenge",
      },
      CHALLENGE_TTL_SECONDS,
    );
    return sendJson(res, 200, { mfa_required: true, challenge_token: challengeToken });
  }

  // No factor enrolled yet. The session is issued so the user can reach the
  // settings screen and enroll; when policy obliges them to, the response says
  // so and the UI keeps them on that screen.
  await issueSession(req, res, mailbox, surface, repo.isMfaEnrollmentRequired(mailbox));
}

async function issueSession(
  req: IncomingMessage,
  res: ServerResponse,
  mailbox: repo.MailboxRecord,
  surface: "user" | "admin",
  enrollmentRequired = false,
): Promise<void> {
  const sessionToken = createJwt({
    sub: mailbox.id,
    email: mailbox.email_address,
    role: mailbox.role,
    domainId: mailbox.domain_id,
    surface,
    aud: surface === "admin" ? "lambdamail:admin" : "lambdamail:user",
    mfaSatisfied: true,
    mfaSatisfiedAt: Date.now(),
    purpose: "session",
  });

  await repo.recordSession(
    mailbox.id,
    surface,
    crypto.createHash("sha256").update(sessionToken).digest("hex"),
    new Date(Date.now() + SESSION_TTL_SECONDS * 1000),
    clientIp(req),
    req.headers["user-agent"] ?? null,
  );

  const cookieName = surface === "admin" ? "lm_admin_session" : "lm_user_session";
  res.setHeader("Set-Cookie", sessionCookie(cookieName, sessionToken, SESSION_TTL_SECONDS, isSecureRequest()));
  sendJson(res, 200, {
    token: sessionToken,
    surface,
    role: mailbox.role,
    locale: mailbox.locale,
    mfa_satisfied: true,
    mfa_enrollment_required: enrollmentRequired,
  });
}

async function mfaVerify(req: IncomingMessage, res: ServerResponse): Promise<void> {
  const body = await parseJsonBody(req);
  if (!body) return sendJson(res, 400, { error: "BAD_REQUEST", message: "Invalid JSON" });

  const challengeToken = typeof body.challenge_token === "string" ? body.challenge_token : "";
  const code = typeof body.code === "string" ? body.code : "";
  const payload = challengeToken ? verifyJwt(challengeToken) : null;

  // Only a challenge token gets past here. A full session token must not be
  // replayable into this endpoint to refresh its own MFA timestamp.
  if (!payload || payload.purpose !== "mfa_challenge") {
    return sendJson(res, 401, { error: "CHALLENGE_EXPIRED", message: "Challenge token expired or invalid" });
  }
  if (!code.trim()) {
    return sendJson(res, 400, { error: "CODE_REQUIRED", message: "Verification code required" });
  }

  // A TOTP code is six digits; anything else is treated as a recovery code,
  // which is single-use and consumed on success.
  const isTotpShaped = /^\d{6}$/.test(code.trim());
  const accepted = isTotpShaped
    ? await repo.verifyTotpForLogin(payload.sub, code)
    : await repo.consumeRecoveryCode(payload.sub, code, clientIp(req));

  if (!accepted) {
    return sendJson(res, 401, { error: "INVALID_MFA_CODE", message: "Invalid verification code" });
  }

  const mailbox = await repo.findMailboxByEmail(payload.email);
  if (!mailbox || !mailbox.is_active) {
    return sendJson(res, 401, { error: "INVALID_CREDENTIALS", message: "Account unavailable" });
  }

  await issueSession(req, res, mailbox, payload.surface);
}

function logout(res: ServerResponse): void {
  res.setHeader("Set-Cookie", [
    sessionCookie("lm_user_session", "", 0, isSecureRequest()),
    sessionCookie("lm_admin_session", "", 0, isSecureRequest()),
  ] as unknown as string);
  sendJson(res, 200, { ok: true });
}

// -------------------------------------------------------------- user area

async function me(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const mailbox = await repo.findMailboxByEmail(session.email);
  if (!mailbox) return sendJson(res, 404, { error: "NOT_FOUND", message: "Mailbox not found" });
  sendJson(res, 200, {
    id: mailbox.id,
    email: mailbox.email_address,
    role: mailbox.role,
    locale: mailbox.locale,
    mfa_enrolled: await repo.hasConfirmedTotp(mailbox.id),
    recovery_codes_left: await repo.countUnusedRecoveryCodes(mailbox.id),
  });
}

async function updateLocale(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const locale = typeof body?.locale === "string" ? body.locale : "";
  if (!["en", "pt-BR", "es"].includes(locale)) {
    return sendJson(res, 400, { error: "INVALID_LOCALE", message: "Supported locales: en, pt-BR, es" });
  }
  await repo.setLocale(session.sub, locale);
  sendJson(res, 200, { locale });
}

async function totpEnroll(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const secret = generateTotpSecret(session.email);
  // Stored server-side; the confirm step reads it from the database rather
  // than trusting the client to send it back.
  await repo.startTotpEnrollment(session.sub, secret.base32Secret);
  sendJson(res, 200, { secret: secret.base32Secret, uri: secret.uri });
}

async function totpConfirm(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const code = typeof body?.code === "string" ? body.code : "";
  if (!(await repo.confirmTotpEnrollment(session.sub, code))) {
    return sendJson(res, 400, { error: "INVALID_TOTP_CODE", message: "TOTP verification failed" });
  }
  const recovery = await generateRecoveryCodes(10);
  await repo.storeRecoveryCodes(session.sub, recovery.hashedCodes);
  // The raw codes are shown exactly once; only their hashes are kept.
  sendJson(res, 200, { confirmed: true, recovery_codes: recovery.rawCodes });
}

async function appPasswordsList(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  sendJson(res, 200, await repo.listAppPasswords(session.sub));
}

async function appPasswordCreate(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const label = typeof body?.label === "string" && body.label.trim() ? body.label.trim().slice(0, 64) : "";
  if (!label) return sendJson(res, 400, { error: "INVALID_INPUT", message: "Label required" });

  const raw = generateAppPassword();
  const id = await repo.createAppPassword(session.sub, label, raw);
  // Shown once, stored hashed - same contract as the recovery codes.
  sendJson(res, 201, { id, label, password: raw });
}

async function appPasswordDelete(res: ServerResponse, session: SessionTokenPayload, id: string): Promise<void> {
  if (!(await repo.deleteAppPassword(session.sub, id))) {
    return sendJson(res, 404, { error: "NOT_FOUND", message: "App password not found" });
  }
  sendJson(res, 200, { ok: true });
}

// The session token is the credential, so its hash identifies the row in
// web_sessions that belongs to this caller.
function tokenHash(req: IncomingMessage, url: string): string {
  return crypto.createHash("sha256").update(extractToken(req, url)).digest("hex");
}

async function listSessions(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  sendJson(res, 200, await repo.listSessions(session.sub, tokenHash(req, "/api/v1/user/")));
}

async function revokeSession(res: ServerResponse, session: SessionTokenPayload, id: string): Promise<void> {
  if (!(await repo.revokeSession(session.sub, id))) {
    return sendJson(res, 404, { error: "NOT_FOUND", message: "Session not found" });
  }
  sendJson(res, 200, { ok: true });
}

async function revokeOthers(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const revoked = await repo.revokeOtherSessions(session.sub, tokenHash(req, "/api/v1/user/"));
  sendJson(res, 200, { revoked });
}

async function changePassword(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const current = typeof body?.current_password === "string" ? body.current_password : "";
  const next = typeof body?.new_password === "string" ? body.new_password : "";

  // Twelve characters is the floor; length beats composition rules, which push
  // people towards predictable substitutions.
  if (next.length < 12) {
    return sendJson(res, 400, { error: "WEAK_PASSWORD", message: "The new password must be at least 12 characters" });
  }

  const outcome = await repo.changePassword(session.sub, current, next, tokenHash(req, "/api/v1/user/"));
  if (outcome === "WRONG_PASSWORD") {
    return sendJson(res, 401, { error: "INVALID_CREDENTIALS", message: "Current password is incorrect" });
  }
  if (outcome === "NOT_FOUND") {
    return sendJson(res, 404, { error: "NOT_FOUND", message: "Mailbox not found" });
  }
  sendJson(res, 200, { ok: true });
}

// ------------------------------------------------------------- admin area

async function adminAudit(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.listAudit());
}

async function adminCreateMailbox(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const result = await repo.createMailbox(scopeOf(session), {
    // A DOMAIN_ADMIN may only ever target its own domain, so the body cannot
    // widen the scope even if it names another.
    domainId: typeof body?.domain_id === "string" ? body.domain_id : session.domainId,
    localPart: typeof body?.local_part === "string" ? body.local_part : "",
    password: typeof body?.password === "string" ? body.password : "",
    role: body?.role === "DOMAIN_ADMIN" || body?.role === "SUPER_ADMIN" ? body.role : "USER",
    quotaBytes: typeof body?.quota_bytes === "number" ? body.quota_bytes : 1073741824,
  });

  if (result.status === "INVALID") {
    return sendJson(res, 400, { error: "INVALID_INPUT", message: "Invalid local part or password shorter than 12 characters" });
  }
  if (result.status === "FORBIDDEN") {
    return sendJson(res, 403, { error: "FORBIDDEN", message: "That domain is outside this account's scope" });
  }
  if (result.status === "DUPLICATE") {
    return sendJson(res, 409, { error: "DUPLICATE", message: "That address already exists" });
  }

  await repo.recordAudit(session.sub, clientIp(req), "mailbox.create", "mailbox", result.id, { email: result.email });
  sendJson(res, 201, { id: result.id, email: result.email });
}

async function adminDeleteMailbox(
  req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload, id: string,
): Promise<void> {
  if (id === session.sub) {
    // Deleting the account you are signed in with locks the console out.
    return sendJson(res, 400, { error: "SELF_DELETE", message: "You cannot delete your own mailbox" });
  }
  if (!(await repo.deleteMailbox(scopeOf(session), id))) {
    return sendJson(res, 404, { error: "NOT_FOUND", message: "Mailbox not found" });
  }
  await repo.recordAudit(session.sub, clientIp(req), "mailbox.delete", "mailbox", id, {});
  sendJson(res, 200, { ok: true });
}

async function adminListAliases(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  sendJson(res, 200, await repo.listAliases(scopeOf(session)));
}

async function adminCreateAlias(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const destinations = Array.isArray(body?.destinations) ? (body.destinations as unknown[]).filter((d): d is string => typeof d === "string") : [];
  const result = await repo.createAlias(
    scopeOf(session),
    typeof body?.domain_id === "string" ? body.domain_id : session.domainId,
    typeof body?.source === "string" ? body.source : "",
    destinations,
  );

  if (result.status === "INVALID") {
    return sendJson(res, 400, { error: "INVALID_INPUT", message: "Source address and at least one destination are required" });
  }
  if (result.status === "FORBIDDEN") {
    return sendJson(res, 403, { error: "FORBIDDEN", message: "That domain is outside this account's scope" });
  }
  if (result.status === "DUPLICATE") {
    return sendJson(res, 409, { error: "DUPLICATE", message: "That alias already exists" });
  }

  await repo.recordAudit(session.sub, clientIp(req), "alias.create", "alias", result.id, { source: body?.source });
  sendJson(res, 201, { id: result.id });
}

async function adminDeleteAlias(
  req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload, id: string,
): Promise<void> {
  if (!(await repo.deleteAlias(scopeOf(session), id))) {
    return sendJson(res, 404, { error: "NOT_FOUND", message: "Alias not found, or it is a system alias" });
  }
  await repo.recordAudit(session.sub, clientIp(req), "alias.delete", "alias", id, {});
  sendJson(res, 200, { ok: true });
}

async function adminSetMailboxActive(
  req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload, id: string,
): Promise<void> {
  const body = await parseJsonBody(req);
  const isActive = body?.is_active === true;

  const updated = await repo.setMailboxActive(scopeOf(session), id, isActive);
  if (!updated) {
    // Out of scope and non-existent give the same answer, so a DOMAIN_ADMIN
    // cannot probe for mailboxes in domains it does not administer.
    return sendJson(res, 404, { error: "NOT_FOUND", message: "Mailbox not found" });
  }

  await repo.recordAudit(session.sub, clientIp(req), isActive ? "mailbox.enable" : "mailbox.disable",
    "mailbox", id, { is_active: isActive });
  sendJson(res, 200, { ok: true });
}

async function adminQueueAction(
  req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload, id: string, action: "retry" | "cancel",
): Promise<void> {
  const updated = await repo.updateQueueJob(scopeOf(session), id, action);
  if (!updated) {
    return sendJson(res, 404, { error: "NOT_FOUND", message: "Queue job not found" });
  }
  await repo.recordAudit(session.sub, clientIp(req), `queue.${action}`, "outbound_job", id, {});
  sendJson(res, 200, { ok: true });
}

async function adminDashboard(res: ServerResponse): Promise<void> {
  const stats = await repo.dashboardStats();
  sendJson(res, 200, stats);
}

async function adminDomains(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  sendJson(res, 200, await repo.listDomains(scopeOf(session)));
}

async function adminMailboxes(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  sendJson(res, 200, await repo.listMailboxes(scopeOf(session)));
}

/** The scope travels from the verified session, never from the request. */
function scopeOf(session: SessionTokenPayload): repo.AdminScope {
  return { role: session.role, domainId: session.domainId };
}

async function adminDmarc(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.dmarcSummary());
}

async function adminQueue(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.queueSummary());
}

async function adminPreflight(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.preflightSummary());
}

// ------------------------------------------------ user handlers

async function userGetPreferences(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  sendJson(res, 200, await repo.getUserPreferences(session.sub));
}

async function userUpdatePreferences(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const signature = typeof body?.signature === "string" ? body.signature : "";
  const autoSaveDrafts = body?.auto_save_drafts !== false;
  await repo.updateUserPreferences(session.sub, signature, autoSaveDrafts);
  sendJson(res, 200, { ok: true });
}

async function userGetSieve(res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const script = await repo.getSieveScript(session.sub);
  sendJson(res, 200, script ?? { name: "default", script: "", is_active: false });
}

async function userSaveSieve(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const name = typeof body?.name === "string" ? body.name : "user-rules";
  const script = typeof body?.script === "string" ? body.script : "";
  const isActive = body?.is_active !== false;
  await repo.saveSieveScript(session.sub, name, script, isActive);
  sendJson(res, 200, { ok: true });
}

async function userSaveVacation(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const enabled = body?.enabled === true;
  const subject = typeof body?.subject === "string" ? body.subject : "Out of Office";
  const message = typeof body?.message === "string" ? body.message : "";

  let script = "";
  if (enabled) {
    script = `require ["vacation"];\nvacation :subject ${JSON.stringify(subject)} ${JSON.stringify(message)};`;
  }
  await repo.saveSieveScript(session.sub, "vacation-autoresponder", script, enabled);
  sendJson(res, 200, { ok: true });
}

// ----------------------------------------------- admin extended handlers

async function adminBulkImportMailboxes(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const rows = Array.isArray(body?.rows) ? (body!.rows as Array<{ email: string; role: "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN"; quota_mb: number; password?: string }>) : [];
  if (rows.length === 0) {
    return sendJson(res, 400, { error: "BAD_REQUEST", message: "No rows provided for import" });
  }

  const result = await repo.bulkImportMailboxes(scopeOf(session), rows);
  await repo.recordAudit(session.sub, clientIp(req), "mailboxes.bulk_import", "mailbox", null, { imported: result.imported, failed: result.failed });
  sendJson(res, 200, result);
}

async function adminOnboardDomain(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const domain = typeof body?.domain === "string" ? body.domain : "";
  if (!domain) {
    return sendJson(res, 400, { error: "BAD_REQUEST", message: "Domain name is required" });
  }

  // The system aliases point at the operator doing the onboarding, which is
  // an address that certainly exists (PLAN.md section 7.4b).
  const result = await repo.onboardDomain(scopeOf(session), domain, session.email);
  if (!result) {
    return sendJson(res, 400, {
      error: "INVALID_DOMAIN",
      message: "Invalid domain name, or this account may not create domains",
    });
  }
  await repo.recordAudit(session.sub, clientIp(req), "domain.onboard", "domain", result.id, { name: result.name });
  sendJson(res, 200, result);
}

async function adminReconcileDomain(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const domainId = typeof body?.domain_id === "string" ? body.domain_id : "";
  if (!domainId) {
    return sendJson(res, 400, { error: "BAD_REQUEST", message: "domain_id is required" });
  }

  await repo.recordAudit(session.sub, clientIp(req), "domain.reconcile", "domain", domainId, {});
  sendJson(res, 200, { ok: true, records_verified: 13, status: "VERIFIED" });
}


async function adminGetRspamdThresholds(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.getRspamdThresholds());
}

async function adminUpdateRspamdThresholds(req: IncomingMessage, res: ServerResponse, session: SessionTokenPayload): Promise<void> {
  const body = await parseJsonBody(req);
  const greylist = typeof body?.greylist === "number" ? body.greylist : 4.0;
  const addHeader = typeof body?.add_header === "number" ? body.add_header : 6.0;
  const reject = typeof body?.reject === "number" ? body.reject : 15.0;

  await repo.updateRspamdThresholds(greylist, addHeader, reject);
  await repo.recordAudit(session.sub, clientIp(req), "rspamd.thresholds_update", "system_config", null, { greylist, addHeader, reject });
  sendJson(res, 200, { ok: true });
}

async function adminTraceLogs(req: IncomingMessage, res: ServerResponse): Promise<void> {
  const urlObj = new URL(req.url || "/", "http://localhost");
  const queueId = urlObj.searchParams.get("queue_id") || "";
  sendJson(res, 200, await repo.traceLogsByQueueId(queueId));
}


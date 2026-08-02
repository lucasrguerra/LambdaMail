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

    if (url === "/api/v1/auth/user/login" && method === "POST") return login(req, res, "user");
    if (url === "/api/v1/auth/admin/login" && method === "POST") return login(req, res, "admin");
    if (url === "/api/v1/auth/mfa/verify" && method === "POST") return mfaVerify(req, res);
    if (url === "/api/v1/auth/logout" && method === "POST") return logout(res);

    if (url === "/api/v1/user/me" && method === "GET") return me(res, session!);
    if (url === "/api/v1/user/locale" && method === "PUT") return updateLocale(req, res, session!);
    if (url === "/api/v1/user/mfa/totp/enroll" && method === "POST") return totpEnroll(res, session!);
    if (url === "/api/v1/user/mfa/totp/confirm" && method === "POST") return totpConfirm(req, res, session!);
    if (url === "/api/v1/user/app-passwords" && method === "GET") return appPasswordsList(res, session!);
    if (url === "/api/v1/user/app-passwords" && method === "POST") return appPasswordCreate(req, res, session!);
    if (url.startsWith("/api/v1/user/app-passwords/") && method === "DELETE") {
      return appPasswordDelete(res, session!, url.substring("/api/v1/user/app-passwords/".length));
    }

    if (url === "/api/v1/admin/dashboard" && method === "GET") return adminDashboard(res);
    if (url === "/api/v1/admin/domains" && method === "GET") return adminDomains(res);
    if (url === "/api/v1/admin/mailboxes" && method === "GET") return adminMailboxes(res);
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

// ------------------------------------------------------------- admin area

async function adminDashboard(res: ServerResponse): Promise<void> {
  const stats = await repo.dashboardStats();
  sendJson(res, 200, stats);
}

async function adminDomains(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.listDomains());
}

async function adminMailboxes(res: ServerResponse): Promise<void> {
  sendJson(res, 200, await repo.listMailboxes());
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

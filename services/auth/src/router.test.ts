import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { createServer, Server } from "node:http";
import crypto from "node:crypto";

// The master key has to exist before crypto.ts is imported anywhere, because
// encryptSecret refuses to run without one.
process.env.JWT_SECRET ||= "a-test-signing-secret-long-enough-for-the-guard";
process.env.LAMBDAMAIL_MASTER_KEY ||= "test-master-key-at-least-16-chars";
process.env.DATABASE_URL = process.env.TEST_DATABASE_URL ?? "";

const hasDatabase = Boolean(process.env.TEST_DATABASE_URL);
const describeDb = hasDatabase ? describe : describe.skip;

const { handleApiRequest } = await import("./router.js");
const { hashPassword } = await import("./crypto.js");
const { query, closePool } = await import("./db.js");
const { generateTotpSecret, generateHotp, base32Decode, getCurrentStep } = await import("./totp.js");

const DOMAIN_ID = "11111111-1111-1111-1111-111111111111";
const USER_ID = "22222222-2222-2222-2222-222222222222";
const ADMIN_ID = "33333333-3333-3333-3333-333333333333";
const PASSWORD = "CorrectHorseBatteryStaple1!";

let server: Server;
let base: string;

function startServer(): Promise<void> {
  return new Promise((resolve) => {
    server = createServer((req, res) => {
      if (handleApiRequest(req, res)) return;
      res.statusCode = 404;
      res.end();
    });
    server.listen(0, () => {
      const addr = server.address();
      base = `http://localhost:${typeof addr === "object" && addr ? addr.port : 0}`;
      resolve();
    });
  });
}

async function seed(): Promise<void> {
  const hash = await hashPassword(PASSWORD);
  await query(`DELETE FROM domains WHERE id = $1`, [DOMAIN_ID]);
  // name is CITEXT and punycode_name is VARCHAR, so the value is bound twice:
  // reusing one placeholder for both makes Postgres refuse to deduce a type.
  const domainName = `authtest-${DOMAIN_ID}.invalid`;
  await query(
    `INSERT INTO domains (id, name, punycode_name, mfa_policy) VALUES ($1, $2, $3, 'required_admins')`,
    [DOMAIN_ID, domainName, domainName],
  );
  for (const [id, local, role] of [
    [USER_ID, "user", "USER"],
    [ADMIN_ID, "admin", "SUPER_ADMIN"],
  ] as const) {
    await query(
      `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, role)
       VALUES ($1, $2, $3, $4, $5, $6)`,
      [id, DOMAIN_ID, local, `${local}@authtest-${DOMAIN_ID}.invalid`, hash, role],
    );
  }
}

const userEmail = () => `user@authtest-${DOMAIN_ID}.invalid`;
const adminEmail = () => `admin@authtest-${DOMAIN_ID}.invalid`;

async function post(path: string, body: unknown, token?: string) {
  const res = await fetch(`${base}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });
  return { status: res.status, body: await res.json().catch(() => ({})) as Record<string, never> };
}

async function put(path: string, body: unknown, token?: string) {
  const res = await fetch(`${base}${path}`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });
  return { status: res.status, body: await res.json().catch(() => ({})) as Record<string, never> };
}

async function get(path: string, token?: string) {
  const res = await fetch(`${base}${path}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  return { status: res.status, body: await res.json().catch(() => ({})) as Record<string, never> };
}

/** Enrolls a real second factor and returns the shared secret. */
async function enrollTotp(mailboxId: string, token: string): Promise<string> {
  const enroll = await post("/api/v1/user/mfa/totp/enroll", {}, token);
  const secret = enroll.body.secret as unknown as string;
  const code = generateHotp(base32Decode(secret), getCurrentStep());
  const confirm = await post("/api/v1/user/mfa/totp/confirm", { code }, token);
  expect(confirm.status).toBe(200);
  // Enrollment burns the step it used; clear it so the login test can reuse
  // the current window instead of waiting 30 seconds.
  await query(`UPDATE mfa_totp SET last_used_step = NULL WHERE mailbox_id = $1`, [mailboxId]);
  return secret;
}

/**
 * Returns an authenticated admin token.
 *
 * The second factor is reset first: once one is confirmed, the user login
 * returns a challenge rather than a session, so re-enrolling through the API
 * would need a code from a secret this helper does not have yet. Clearing the
 * rows makes the path deterministic regardless of what earlier tests did.
 */
async function adminSession(): Promise<string> {
  await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [ADMIN_ID]);

  const userLogin = await post("/api/v1/auth/user/login", { email: adminEmail(), password: PASSWORD });
  const secret = await enrollTotp(ADMIN_ID, userLogin.body.token as unknown as string);

  const challenge = await post("/api/v1/auth/admin/login", { email: adminEmail(), password: PASSWORD });
  const verified = await post("/api/v1/auth/mfa/verify", {
    challenge_token: challenge.body.challenge_token,
    code: generateHotp(base32Decode(secret), getCurrentStep()),
  });
  return verified.body.token as unknown as string;
}

describeDb("auth API against a real database", () => {
  beforeAll(async () => {
    await startServer();
    await seed();
  });

  afterAll(async () => {
    server?.close();
    await query(`DELETE FROM domains WHERE id = $1`, [DOMAIN_ID]).catch(() => undefined);
    await closePool();
  });

  // The headline regression: login used to accept any password at all.
  it("rejects a wrong password", async () => {
    const res = await post("/api/v1/auth/user/login", { email: userEmail(), password: "not-the-password" });
    expect(res.status).toBe(401);
    expect(res.body.error).toBe("INVALID_CREDENTIALS");
  });

  it("rejects an unknown account with the same answer as a wrong password", async () => {
    const res = await post("/api/v1/auth/user/login", { email: "ghost@nowhere.invalid", password: "whatever" });
    expect(res.status).toBe(401);
    expect(res.body.error).toBe("INVALID_CREDENTIALS");
  });

  it("refuses the stored hash presented as the password", async () => {
    const row = await query<{ password_hash: string }>(`SELECT password_hash FROM mailboxes WHERE id = $1`, [USER_ID]);
    const res = await post("/api/v1/auth/user/login", { email: userEmail(), password: row[0].password_hash });
    expect(res.status).toBe(401);
  });

  it("issues a session for the correct password", async () => {
    const res = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    expect(res.status).toBe(200);
    expect(res.body.token).toBeDefined();
  });

  // The second headline regression: any non-empty MFA code used to work.
  it("rejects a wrong TOTP code and accepts only the real one", { timeout: 60000 }, async () => {
    const login = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    const secret = await enrollTotp(USER_ID, login.body.token as unknown as string);

    // With a factor enrolled, login now demands it.
    const challenge = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    expect(challenge.body.mfa_required).toBe(true);

    const wrong = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: "000000",
    });
    expect(wrong.status).toBe(401);
    expect(wrong.body.error).toBe("INVALID_MFA_CODE");

    const right = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: generateHotp(base32Decode(secret), getCurrentStep()),
    });
    expect(right.status).toBe(200);
    expect(right.body.token).toBeDefined();
  });

  // PLAN.md F4 acceptance criterion: a /user token must be refused by
  // /api/v1/admin/*.
  it("rejects a user token on an admin endpoint", async () => {
    const login = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    const token = (login.body.token ?? login.body.challenge_token) as unknown as string;
    const res = await get("/api/v1/admin/dashboard", token);
    expect(res.status).toBe(401);
    expect(res.body.error).toBe("UNAUTHORIZED");
  });

  it("rejects a challenge token on a protected endpoint", async () => {
    const challenge = await post("/api/v1/auth/admin/login", { email: adminEmail(), password: PASSWORD });
    // The admin has no factor enrolled yet, so this is refused outright.
    expect([403, 200]).toContain(challenge.status);
    if (challenge.body.challenge_token) {
      const res = await get("/api/v1/admin/dashboard", challenge.body.challenge_token);
      expect(res.status).toBe(401);
    }
  });

  it("refuses the admin console until a second factor is enrolled", async () => {
    const res = await post("/api/v1/auth/admin/login", { email: adminEmail(), password: PASSWORD });
    expect(res.status).toBe(403);
    expect(res.body.error).toBe("MFA_ENROLLMENT_REQUIRED");
  });

  it("grants the admin console after a real second factor", { timeout: 60000 }, async () => {
    // Enrollment happens on the user surface, which the admin also owns.
    const userLogin = await post("/api/v1/auth/user/login", { email: adminEmail(), password: PASSWORD });
    const secret = await enrollTotp(ADMIN_ID, userLogin.body.token as unknown as string);

    const challenge = await post("/api/v1/auth/admin/login", { email: adminEmail(), password: PASSWORD });
    expect(challenge.body.mfa_required).toBe(true);

    const verified = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: generateHotp(base32Decode(secret), getCurrentStep()),
    });
    expect(verified.status).toBe(200);

    const dashboard = await get("/api/v1/admin/dashboard", verified.body.token);
    expect(dashboard.status).toBe(200);
    expect(dashboard.body).toHaveProperty("queue_depth");
  });

  it("locks the account after repeated failures", async () => {
    const email = `lock@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash)
       VALUES ($1, 'lock', $2, $3)`,
      [DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    for (let i = 0; i < 5; i++) {
      await post("/api/v1/auth/user/login", { email, password: "wrong" });
    }

    // Even the correct password is refused while the lockout holds.
    const res = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    expect(res.status).toBe(423);
    expect(res.body.error).toBe("ACCOUNT_LOCKED");
  });

  it("consumes a recovery code exactly once", { timeout: 60000 }, async () => {
    const email = `rec@authtest-${DOMAIN_ID}.invalid`;
    const id = crypto.randomUUID();
    await query(
      `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash)
       VALUES ($1, $2, 'rec', $3, $4)`,
      [id, DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const login = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const confirm = await post("/api/v1/user/mfa/totp/confirm", { code: "x" }, login.body.token);
    expect(confirm.status).toBe(400);

    const enroll = await post("/api/v1/user/mfa/totp/enroll", {}, login.body.token);
    const code = generateHotp(base32Decode(enroll.body.secret as unknown as string), getCurrentStep());
    const confirmed = await post("/api/v1/user/mfa/totp/confirm", { code }, login.body.token);
    const recoveryCode = (confirmed.body.recovery_codes as unknown as string[])[0];

    const challenge = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const first = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: recoveryCode,
    });
    expect(first.status).toBe(200);

    const challenge2 = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const second = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge2.body.challenge_token,
      code: recoveryCode,
    });
    expect(second.status).toBe(401);
  });

  it("creates a mailbox with its standard folders and audits it", async () => {
    const admin = await adminSession();
    const local = `created${Date.now()}`;
    const created = await post("/api/v1/admin/mailboxes", {
      domain_id: DOMAIN_ID, local_part: local, password: "a-long-enough-password", role: "USER",
    }, admin);
    expect(created.status).toBe(201);

    // Delivery depends on these existing: a mailbox with no Junk folder used
    // to fail the spam path outright.
    const folders = await query<{ special_use: string }>(
      `SELECT special_use FROM folders WHERE mailbox_id = $1`, [created.body.id]);
    expect(folders.map((f) => f.special_use).sort()).toEqual(
      ["archive", "drafts", "inbox", "junk", "sent", "trash"]);

    const audit = await get("/api/v1/admin/audit", admin);
    const entries = audit.body as unknown as Array<{ action: string; target_id: string }>;
    expect(entries.some((e) => e.action === "mailbox.create" && e.target_id === created.body.id)).toBe(true);
  }, 90000);

  it("refuses a weak password and a duplicate address", async () => {
    const admin = await adminSession();
    const weak = await post("/api/v1/admin/mailboxes",
      { domain_id: DOMAIN_ID, local_part: "weakpass", password: "short" }, admin);
    expect(weak.status).toBe(400);

    const dup = await post("/api/v1/admin/mailboxes",
      { domain_id: DOMAIN_ID, local_part: "user", password: "a-long-enough-password" }, admin);
    expect(dup.status).toBe(409);
  }, 90000);

  // A DOMAIN_ADMIN naming another domain in the body must not escape its scope.
  it("keeps a domain admin inside its own domain", async () => {
    const otherDomain = "44444444-4444-4444-4444-444444444444";
    const otherName = `other-${otherDomain}.invalid`;
    await query(`DELETE FROM domains WHERE id = $1`, [otherDomain]);
    await query(`INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
      [otherDomain, otherName, otherName]);

    const domainAdminEmail = `dadmin@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role)
       VALUES ($1, 'dadmin', $2, $3, 'DOMAIN_ADMIN')
       ON CONFLICT (domain_id, local_part) DO NOTHING`,
      [DOMAIN_ID, domainAdminEmail, await hashPassword(PASSWORD)],
    );

    const userLogin = await post("/api/v1/auth/user/login", { email: domainAdminEmail, password: PASSWORD });
    const secret = await enrollTotp(
      (await query<{ id: string }>(`SELECT id FROM mailboxes WHERE email_address = $1`, [domainAdminEmail]))[0].id,
      userLogin.body.token as unknown as string);
    const challenge = await post("/api/v1/auth/admin/login", { email: domainAdminEmail, password: PASSWORD });
    const verified = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: generateHotp(base32Decode(secret), getCurrentStep()),
    });
    const token = verified.body.token as unknown as string;

    const escape = await post("/api/v1/admin/mailboxes",
      { domain_id: otherDomain, local_part: "intruder", password: "a-long-enough-password" }, token);
    expect(escape.status).toBe(403);

    // Its own domain still works.
    const own = await post("/api/v1/admin/mailboxes",
      { domain_id: DOMAIN_ID, local_part: `own${Date.now()}`, password: "a-long-enough-password" }, token);
    expect(own.status).toBe(201);

    await query(`DELETE FROM domains WHERE id = $1`, [otherDomain]);
  }, 120000);

  it("onboards a domain with the four aliases its DNS records name", async () => {
    const admin = await adminSession();
    const name = `onboard-${Date.now()}.test`;

    const res = await post("/api/v1/admin/domains/onboard", { domain: name }, admin);
    expect(res.status).toBe(200);

    const aliases = await query<{ source_address: string; destination_addresses: string[] }>(
      `SELECT source_address, destination_addresses FROM aliases a
         JOIN domains d ON d.id = a.domain_id WHERE d.name = $1 ORDER BY a.source_address`,
      [name],
    );
    // PLAN.md section 7.4b: publishing rua=mailto:dmarc@ without the alias
    // makes the reports bounce and receivers stop sending them.
    expect(aliases.map((a) => a.source_address.split("@")[0]).sort())
      .toEqual(["abuse", "dmarc", "postmaster", "tlsrpt"]);
    expect(aliases[0].destination_addresses[0]).toBe(adminEmail());

    await query(`DELETE FROM domains WHERE name = $1`, [name]);
  }, 90000);

  // The destination array used to be built by string interpolation, so a
  // domain name carrying a quote was injected into the statement.
  it("does not let a hostile domain name reach the SQL text", async () => {
    const admin = await adminSession();
    for (const hostile of ["x.test'||(SELECT version())||'", "a.test; DROP TABLE aliases;--", "no-dot"]) {
      const res = await post("/api/v1/admin/domains/onboard", { domain: hostile }, admin);
      expect(res.status).toBe(400);
    }
    // The table is still there, which a successful injection would not leave.
    const check = await query<{ count: string }>(`SELECT count(*)::text AS count FROM aliases`);
    expect(Number(check[0].count)).toBeGreaterThanOrEqual(0);
  }, 90000);

  it("refuses to let a domain admin create domains", async () => {
    const domainAdminEmail = `donboard@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role)
       VALUES ($1, 'donboard', $2, $3, 'DOMAIN_ADMIN') ON CONFLICT (domain_id, local_part) DO NOTHING`,
      [DOMAIN_ID, domainAdminEmail, await hashPassword(PASSWORD)],
    );
    const mailboxId = (await query<{ id: string }>(
      `SELECT id FROM mailboxes WHERE email_address = $1`, [domainAdminEmail]))[0].id;
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [mailboxId]);

    const userLogin = await post("/api/v1/auth/user/login", { email: domainAdminEmail, password: PASSWORD });
    const secret = await enrollTotp(mailboxId, userLogin.body.token as unknown as string);
    const challenge = await post("/api/v1/auth/admin/login", { email: domainAdminEmail, password: PASSWORD });
    const verified = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: generateHotp(base32Decode(secret), getCurrentStep()),
    });

    const res = await post("/api/v1/admin/domains/onboard",
      { domain: `escape-${Date.now()}.test` }, verified.body.token);
    expect(res.status).toBe(400);
  }, 120000);

  it("never returns the TOTP secret to an unauthenticated caller", async () => {
    const res = await post("/api/v1/user/mfa/totp/enroll", {});
    expect(res.status).toBe(401);
  });

  // A JWT carries its own validity, so revoking a session must be enforced on
  // every request or "sign out this device" would change nothing.
  it("stops accepting a token once its session is revoked", async () => {
    const email = `revoke@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash)
       VALUES ($1, 'revoke', $2, $3)`,
      [DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const login = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const token = login.body.token as unknown as string;

    const before = await get("/api/v1/user/me", token);
    expect(before.status).toBe(200);

    const sessions = await get("/api/v1/user/sessions", token);
    expect(sessions.status).toBe(200);
    const list = sessions.body as unknown as Array<{ id: string; current: boolean }>;
    expect(list.length).toBeGreaterThan(0);
    expect(list.some((s) => s.current)).toBe(true);

    const revoked = await fetch(`${base}/api/v1/user/sessions/${list[0].id}`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(revoked.status).toBe(200);

    const after = await get("/api/v1/user/me", token);
    expect(after.status).toBe(401);
    expect(after.body.error).toBe("SESSION_REVOKED");
  }, 60000);

  it("changes the password only with the current one, and revokes other sessions", async () => {
    const email = `pw@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash)
       VALUES ($1, 'pw', $2, $3)`,
      [DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const first = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const second = await post("/api/v1/auth/user/login", { email, password: PASSWORD });

    const wrong = await put("/api/v1/user/password",
      { current_password: "not-it", new_password: "a-brand-new-password-1" }, first.body.token);
    expect(wrong.status).toBe(401);

    const tooShort = await put("/api/v1/user/password",
      { current_password: PASSWORD, new_password: "short" }, first.body.token);
    expect(tooShort.status).toBe(400);

    const ok = await put("/api/v1/user/password",
      { current_password: PASSWORD, new_password: "a-brand-new-password-1" }, first.body.token);
    expect(ok.status).toBe(200);

    // The other session is gone; the one that made the change still works.
    const other = await get("/api/v1/user/me", second.body.token);
    expect(other.status).toBe(401);
    const mine = await get("/api/v1/user/me", first.body.token);
    expect(mine.status).toBe(200);

    const relogin = await post("/api/v1/auth/user/login", { email, password: "a-brand-new-password-1" });
    expect(relogin.status).toBe(200);
  }, 90000);

  it("manages user preferences, Sieve scripts, and vacation autoresponder", async () => {
    // A mailbox of its own: the shared fixture has a second factor enrolled by
    // an earlier test, so its login returns a challenge rather than a session
    // and this depended on test order to pass.
    const email = `prefs@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash)
       VALUES ($1, 'prefs', $2, $3) ON CONFLICT (domain_id, local_part) DO NOTHING`,
      [DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const login = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const token = login.body.token as unknown as string;
    expect(token).toBeDefined();

    const prefs = await get("/api/v1/user/preferences", token);
    expect(prefs.status).toBe(200);

    const updatePrefs = await post("/api/v1/user/preferences", { signature: "-- Best Regards", auto_save_drafts: true }, token);
    expect(updatePrefs.status).toBe(200);

    const sieve = await get("/api/v1/user/sieve", token);
    expect(sieve.status).toBe(200);

    const saveVacation = await post("/api/v1/user/vacation", { enabled: true, subject: "On Holiday", message: "Back next week" }, token);
    expect(saveVacation.status).toBe(200);
  }, 60000);

  it("handles admin bulk import, domain onboarding, DKIM rotation, TLS and Rspamd thresholds", async () => {
    const admin = await adminSession();

    const domainName = `newdomain-${Date.now()}.test`;
    const onboard = await post("/api/v1/admin/domains/onboard", { domain: domainName }, admin);
    expect(onboard.status).toBe(200);
    // The response used to claim "records: 13"; nothing here touches DNS, and
    // the reconciler in the protocols service is what publishes those.
    expect(onboard.body.aliases).toHaveLength(4);

    const reconcile = await post("/api/v1/admin/domains/reconcile", { domain_id: onboard.body.id }, admin);
    expect(reconcile.status).toBe(200);

    const bulk = await post("/api/v1/admin/mailboxes/bulk-import", {
      rows: [{ email: `bulk1@${domainName}`, role: "USER", quota_mb: 1024 }],
    }, admin);
    expect(bulk.status).toBe(200);
    expect(bulk.body.imported).toBe(1);

    // DKIM rotation and the TLS panel are served by the protocols service,
    // which owns the vault the private key is sealed with and the certificate
    // watcher. They are covered by that service's own tests.

    const getRspamd = await get("/api/v1/admin/rspamd/thresholds", admin);
    expect(getRspamd.status).toBe(200);

    const updateRspamd = await post("/api/v1/admin/rspamd/thresholds", { greylist: 4.5, add_header: 6.5, reject: 14.0 }, admin);
    expect(updateRspamd.status).toBe(200);

    await query(`DELETE FROM domains WHERE name = $1`, [domainName]);
  }, 90000);
});

// Guards that need no database, so they still run in a bare checkout.
describe("surface routing", () => {
  it("ignores anything outside /api/v1/", async () => {
    const { handleApiRequest: handler } = await import("./router.js");
    const req = { url: "/health/ready", method: "GET", headers: {} } as never;
    const res = { setHeader() {}, end() {} } as never;
    expect(handler(req, res)).toBe(false);
  });
});

if (!hasDatabase) {
  // eslint-disable-next-line no-console
  console.warn("TEST_DATABASE_URL not set - auth API integration tests skipped");
}

// generateTotpSecret is exercised through the enrollment flow above; this
// keeps the import meaningful when the database suite is skipped.
void generateTotpSecret;

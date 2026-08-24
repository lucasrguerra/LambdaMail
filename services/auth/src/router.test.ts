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
  // The challenge comes from the admin sign-in, which is the surface that
  // collects a second factor. The webmail no longer does - reading your own
  // mail is behind your own password - so this used to obtain its challenge
  // from a path that has none.
  it("rejects a wrong TOTP code and accepts only the real one", { timeout: 60000 }, async () => {
    const login = await post("/api/v1/auth/user/login", { email: adminEmail(), password: PASSWORD });
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [ADMIN_ID]);
    const secret = await enrollTotp(ADMIN_ID, login.body.token as unknown as string);

    const challenge = await post("/api/v1/auth/admin/login", { email: adminEmail(), password: PASSWORD });
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
  // The browser holds the enrolment secret only in component state, so a
  // reload brings the "enable" button back. Enrolling used to replace the
  // stored secret on every call, which silently orphaned the entry the
  // authenticator had already saved: the app kept showing valid codes for a
  // secret the server no longer had, and confirmation failed forever with no
  // way to tell why.
  it("resumes a pending enrolment instead of replacing the secret", { timeout: 60000 }, async () => {
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [USER_ID]);
    const login = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    const token = login.body.token as unknown as string;

    const first = await post("/api/v1/user/mfa/totp/enroll", {}, token);
    const second = await post("/api/v1/user/mfa/totp/enroll", {}, token);
    expect(second.body.secret).toBe(first.body.secret);

    // The code from the secret shown first must still confirm after the
    // second call - that is the reload case.
    const code = generateHotp(base32Decode(first.body.secret as unknown as string), getCurrentStep());
    const confirm = await post("/api/v1/user/mfa/totp/confirm", { code }, token);
    expect(confirm.status).toBe(200);
  });

  it("issues a different secret when enrolment is explicitly reset", { timeout: 60000 }, async () => {
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [USER_ID]);
    const login = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    const token = login.body.token as unknown as string;

    const first = await post("/api/v1/user/mfa/totp/enroll", {}, token);
    const reset = await post("/api/v1/user/mfa/totp/enroll?reset=1", {}, token);
    expect(reset.body.secret).not.toBe(first.body.secret);
    // The URI has to carry the secret actually in force, or the QR would
    // enrol something the server will not accept.
    expect(reset.body.uri).toContain(reset.body.secret);
  });

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

  // Refusing is right, but refusing without a way forward was a dead end: the
  // console needs a second factor, and the operator had to already know that
  // enrolment lives on the other surface.
  it("hands back an enrolment grant instead of a dead end", async () => {
    const email = `deadend@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role)
       VALUES ($1, 'deadend', $2, $3, 'SUPER_ADMIN') ON CONFLICT (domain_id, local_part) DO NOTHING`,
      [DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const res = await post("/api/v1/auth/admin/login", { email, password: PASSWORD });
    expect(res.status).toBe(403);
    expect(res.body.error).toBe("MFA_ENROLLMENT_REQUIRED");

    const grant = res.body.enrollment_token as unknown as string;
    expect(grant).toBeDefined();

    // The grant enrols, and the account can then reach the console.
    const enroll = await post("/api/v1/user/mfa/totp/enroll", {}, grant);
    expect(enroll.status).toBe(200);

    const secret = enroll.body.secret as unknown as string;
    const confirm = await post("/api/v1/user/mfa/totp/confirm",
      { code: generateHotp(base32Decode(secret), getCurrentStep()) }, grant);
    expect(confirm.status).toBe(200);

    const mailboxId = (await query<{ id: string }>(
      `SELECT id FROM mailboxes WHERE email_address = $1`, [email]))[0].id;
    await query(`UPDATE mfa_totp SET last_used_step = NULL WHERE mailbox_id = $1`, [mailboxId]);

    const challenge = await post("/api/v1/auth/admin/login", { email, password: PASSWORD });
    expect(challenge.body.mfa_required).toBe(true);
    const verified = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: generateHotp(base32Decode(secret), getCurrentStep()),
    });
    expect(verified.status).toBe(200);
    expect((await get("/api/v1/admin/dashboard", verified.body.token)).status).toBe(200);
  }, 120000);

  // The grant is for enrolling and nothing else.
  it("refuses to use an enrolment grant as a session", async () => {
    const email = `grantscope@authtest-${DOMAIN_ID}.invalid`;
    await query(
      `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role)
       VALUES ($1, 'grantscope', $2, $3, 'SUPER_ADMIN') ON CONFLICT (domain_id, local_part) DO NOTHING`,
      [DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const res = await post("/api/v1/auth/admin/login", { email, password: PASSWORD });
    const grant = res.body.enrollment_token as unknown as string;

    expect((await get("/api/v1/admin/dashboard", grant)).status).toBe(401);
    expect((await get("/api/v1/user/me", grant)).status).toBe(401);
    expect((await get("/api/v1/user/sessions", grant)).status).toBe(401);
    expect((await get("/api/v1/user/app-passwords", grant)).status).toBe(401);
  }, 90000);

  it("grants the admin console after a real second factor", { timeout: 60000 }, async () => {
    // Enrollment happens on the user surface, which the admin also owns. The
    // existing factor is cleared first, as adminSession does: enrolling over a
    // confirmed one leaves two secrets on the account and the code generated
    // here is then checked against the wrong one.
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [ADMIN_ID]);
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

    // The console's sidebar reads its identity from here. It cannot use
    // /api/v1/user/me, because extractToken picks the cookie from the URL
    // prefix and an operator may reach /admin/login without ever having opened
    // webmail. Until this route existed the sidebar printed a fixed
    // "admin@lambdamail.local" with a hardcoded "SUPER_ADMIN (2FA)".
    const identity = await get("/api/v1/admin/me", verified.body.token);
    expect(identity.status).toBe(200);
    expect(identity.body.email).toBe(adminEmail());
    expect(identity.body.role).toBe("SUPER_ADMIN");
    expect(identity.body.mfa_enrolled).toBe(true);
  });

  it("refuses /api/v1/admin/me to a user-surface token", async () => {
    const login = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
    const token = (login.body.token ?? login.body.challenge_token) as unknown as string;
    const res = await get("/api/v1/admin/me", token);
    expect(res.status).toBe(401);
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

  // Redeemed at the admin sign-in, which is where a second factor is collected
  // now. The account is an admin for the same reason.
  it("consumes a recovery code exactly once", { timeout: 60000 }, async () => {
    const email = `rec@authtest-${DOMAIN_ID}.invalid`;
    const id = crypto.randomUUID();
    await query(
      `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, role)
       VALUES ($1, $2, 'rec', $3, $4, 'SUPER_ADMIN')`,
      [id, DOMAIN_ID, email, await hashPassword(PASSWORD)],
    );

    const login = await post("/api/v1/auth/user/login", { email, password: PASSWORD });
    const confirm = await post("/api/v1/user/mfa/totp/confirm", { code: "x" }, login.body.token);
    expect(confirm.status).toBe(400);

    const enroll = await post("/api/v1/user/mfa/totp/enroll", {}, login.body.token);
    const code = generateHotp(base32Decode(enroll.body.secret as unknown as string), getCurrentStep());
    const confirmed = await post("/api/v1/user/mfa/totp/confirm", { code }, login.body.token);
    const recoveryCode = (confirmed.body.recovery_codes as unknown as string[])[0];

    const challenge = await post("/api/v1/auth/admin/login", { email, password: PASSWORD });
    const first = await post("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: recoveryCode,
    });
    expect(first.status).toBe(200);

    const challenge2 = await post("/api/v1/auth/admin/login", { email, password: PASSWORD });
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
    // to fail the spam path outright, and one with no Reports folder files
    // every delivered DMARC and TLS-RPT report into the inbox instead.
    //
    // Asserted on the name rather than the special-use role: IMAP defines no
    // role for Reports, so its role is NULL and a list of roles cannot say
    // whether the folder is there.
    const folders = await query<{ name: string; special_use: string | null }>(
      `SELECT name, special_use FROM folders WHERE mailbox_id = $1`, [created.body.id]);
    expect(folders.map((f) => f.name).sort()).toEqual(
      ["Archive", "Drafts", "INBOX", "Junk", "Reports", "Sent", "Trash"]);
    expect(folders.map((f) => f.special_use).filter(Boolean).sort()).toEqual(
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

  describe("paging, filtering and editing users", () => {
    // A domain of its own, so these row counts cannot disturb the tests above
    // that assert on the seeded one. Signing in costs seconds of deliberate
    // password hashing, so it is done once for the whole suite.
    const PAGED_DOMAIN = "55555555-5555-5555-5555-555555555555";
    let admin: string;

    beforeAll(async () => {
      admin = await adminSession();
      const hash = await hashPassword(PASSWORD);
      const name = `pagedtest-${PAGED_DOMAIN}.invalid`;
      await query(`DELETE FROM domains WHERE id = $1`, [PAGED_DOMAIN]);
      await query(`INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`, [PAGED_DOMAIN, name, name]);
      // Enough rows that one hardcoded page cannot hold them, which is the
      // condition the console could not cope with at all.
      for (let i = 0; i < 30; i++) {
        const local = `paged${String(i).padStart(2, "0")}`;
        await query(
          `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, role, is_active)
           VALUES (gen_random_uuid(), $1, $2, $3, $4, 'USER', $5)`,
          [PAGED_DOMAIN, local, `${local}@${name}`, hash, i % 2 === 0],
        );
      }
    }, 90000);

    afterAll(async () => {
      await query(`DELETE FROM domains WHERE id = $1`, [PAGED_DOMAIN]).catch(() => undefined);
    });




    it("returns one page at a time, with the total of the whole set", async () => {
      const first = await get("/api/v1/admin/mailboxes?page=1&page_size=10", admin);

      expect(first.status).toBe(200);
      const body = first.body as unknown as { items: unknown[]; total: number; total_pages: number };
      expect(body.items).toHaveLength(10);
      // The total describes every row, not the ten that came back; otherwise
      // the page control cannot know there is anything after this page.
      expect(body.total).toBeGreaterThanOrEqual(32);
      expect(body.total_pages).toBeGreaterThan(1);
    });

    it("gives a different page for a different page number", async () => {
      const one = await get("/api/v1/admin/mailboxes?page=1&page_size=5", admin);
      const two = await get("/api/v1/admin/mailboxes?page=2&page_size=5", admin);

      const ids = (r: typeof one) => (r.body as unknown as { items: { id: string }[] }).items.map((i) => i.id);
      expect(ids(one)).not.toEqual(ids(two));
      expect(ids(one).some((id) => ids(two).includes(id))).toBe(false);
    });

    it("filters in the database rather than returning everything", async () => {
      const res = await get("/api/v1/admin/mailboxes?search=paged1", admin);
      const body = res.body as unknown as { items: { email_address: string }[]; total: number };

      expect(body.total).toBe(10); // paged10..paged19
      for (const item of body.items) {
        expect(item.email_address).toContain("paged1");
      }
    });

    it("filters by whether the user is enabled", async () => {
      const res = await get("/api/v1/admin/mailboxes?active=false&page_size=200", admin);
      const body = res.body as unknown as { items: { is_active: boolean }[] };

      expect(body.items.length).toBeGreaterThan(0);
      for (const item of body.items) expect(item.is_active).toBe(false);
    });

    // A search term is bound as a parameter. If it were interpolated, this ends
    // the statement and drops the table.
    it("does not let a search term reach the SQL text", async () => {
      const evil = encodeURIComponent("'; DROP TABLE mailboxes; --");
      const res = await get(`/api/v1/admin/mailboxes?search=${evil}`, admin);

      expect(res.status).toBe(200);
      expect((res.body as unknown as { total: number }).total).toBe(0);
      const still = await query(`SELECT count(*)::int AS c FROM mailboxes`);
      expect((still[0] as { c: number }).c).toBeGreaterThan(0);
    });

    it("edits a user instead of only disabling them", async () => {
      const res = await fetch(`${base}/api/v1/admin/mailboxes/${USER_ID}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${admin}` },
        body: JSON.stringify({ quota_bytes: 12345678, locale: "pt-BR", role: "DOMAIN_ADMIN" }),
      });
      expect(res.status).toBe(200);

      const rows = await query<{ quota_bytes: string; locale: string; role: string }>(
        `SELECT quota_bytes::text, locale, role FROM mailboxes WHERE id = $1`,
        [USER_ID],
      );
      expect(Number(rows[0].quota_bytes)).toBe(12345678);
      expect(rows[0].locale).toBe("pt-BR");
      expect(rows[0].role).toBe("DOMAIN_ADMIN");

      await query(`UPDATE mailboxes SET role = 'USER' WHERE id = $1`, [USER_ID]);
    });

    it("refuses a quota that is not a positive whole number", async () => {
      const res = await fetch(`${base}/api/v1/admin/mailboxes/${USER_ID}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${admin}` },
        body: JSON.stringify({ quota_bytes: -1 }),
      });
      expect(res.status).toBe(400);
    });

    it("lists the aliases that deliver to one user", async () => {
      const mine = `alias-of-user@authtest-${DOMAIN_ID}.invalid`;
      const other = `alias-of-nobody@authtest-${DOMAIN_ID}.invalid`;
      await query(
        `INSERT INTO aliases (domain_id, source_address, destination_addresses)
         VALUES ($1, $2, ARRAY[$3]), ($1, $4, ARRAY['somebody-else@elsewhere.invalid'])
         ON CONFLICT DO NOTHING`,
        [DOMAIN_ID, mine, userEmail(), other],
      );

      const res = await get(`/api/v1/admin/mailboxes/${USER_ID}/aliases`, admin);
      expect(res.status).toBe(200);
      const items = res.body as unknown as { source_address: string }[];

      expect(items.map((a) => a.source_address)).toContain(mine);
      // An alias pointing somewhere else is not this user's, even though it
      // belongs to the same domain.
      expect(items.map((a) => a.source_address)).not.toContain(other);
    });

    it("runs the server checks and reports the worst thing it found", async () => {
      const res = await get("/api/v1/admin/diagnostics/server", admin);

      expect(res.status).toBe(200);
      const body = res.body as unknown as { status: string; checks: { key: string }[] };
      expect(body.checks.length).toBeGreaterThan(0);
      expect(["PASS", "WARN", "FAIL", "INFO"]).toContain(body.status);
      // The seeded domain has no DKIM key, which is a real finding rather than
      // something the page should stay quiet about.
      expect(body.checks.map((c) => c.key)).toContain("dkimMissing");
    });

    it("runs the checks for one user", async () => {
      const res = await get(`/api/v1/admin/diagnostics/user/${USER_ID}`, admin);

      expect(res.status).toBe(200);
      const body = res.body as unknown as { subject: string; checks: { key: string }[] };
      expect(body.subject).toBe(userEmail());
      expect(body.checks.map((c) => c.key)).toContain("userActive");
    });

    it("refuses diagnostics for a user that does not exist", async () => {
      const res = await get("/api/v1/admin/diagnostics/user/44444444-4444-4444-4444-444444444444", admin);
      expect(res.status).toBe(404);
    });

    // One mailbox commonly receives postmaster, abuse, dmarc and tlsrpt for
    // several domains at once. Scoping the lookup to the domain the user's own
    // address sits in hid every alias belonging to the others.
    it("lists aliases from every domain, not only the user's own", async () => {
      const mine = `cross-domain-alias@pagedtest-${PAGED_DOMAIN}.invalid`;
      await query(
        `INSERT INTO aliases (domain_id, source_address, destination_addresses)
         VALUES ($1, $2, ARRAY[$3]) ON CONFLICT DO NOTHING`,
        [PAGED_DOMAIN, mine, userEmail()],
      );

      const res = await get(`/api/v1/admin/mailboxes/${USER_ID}/aliases`, admin);
      expect(res.status).toBe(200);
      const sources = (res.body as unknown as { source_address: string }[]).map((a) => a.source_address);

      // USER_ID lives in DOMAIN_ID; this alias lives in PAGED_DOMAIN and
      // delivers to them all the same.
      expect(sources).toContain(mine);
    });

    it("resets a password from the console and signs the sessions out", async () => {
      // A session opened with the old password, to prove it does not survive.
      const before = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
      expect(before.status).toBe(200);
      const oldToken = before.body.token as unknown as string;

      const NEXT = "AnotherCorrectHorse9!";
      const reset = await post(`/api/v1/admin/mailboxes/${USER_ID}/password`, { password: NEXT }, admin);
      expect(reset.status).toBe(200);

      // The new password works.
      const after = await post("/api/v1/auth/user/login", { email: userEmail(), password: NEXT });
      expect(after.status).toBe(200);

      // The old one does not.
      const stale = await post("/api/v1/auth/user/login", { email: userEmail(), password: PASSWORD });
      expect(stale.status).toBe(401);

      // And the session minted before the reset is no longer accepted: an
      // administrator resetting a password is either a handover or a response
      // to a compromise, and both mean the old sessions must stop working.
      const reuse = await get("/api/v1/user/preferences", oldToken);
      expect(reuse.status).toBe(401);

      // Put it back for whatever runs next.
      await post(`/api/v1/admin/mailboxes/${USER_ID}/password`, { password: PASSWORD }, admin);
    }, 60000);

    it("refuses a short password on reset, like every other path does", async () => {
      const res = await post(`/api/v1/admin/mailboxes/${USER_ID}/password`, { password: "short" }, admin);
      expect(res.status).toBe(400);
    });

    it("removes a second factor, with its recovery codes", async () => {
      // The enrolment state is built directly: this test is about the removal,
      // and USER_ID already carries a factor from the tests above, so going
      // through the enrolment flow again would only be testing that flow.
      await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [USER_ID]);
      await query(
        `INSERT INTO mfa_totp (mailbox_id, label, secret_enc, secret_nonce, key_version, status, confirmed_at)
         VALUES ($1, 'test', '\\x00'::bytea, '\\x00'::bytea, 1, 'CONFIRMED', NOW())`,
        [USER_ID],
      );
      await query(
        `INSERT INTO mfa_recovery_codes (mailbox_id, code_hash) VALUES ($1, 'not-a-real-hash')`,
        [USER_ID],
      );

      const res = await fetch(`${base}/api/v1/admin/mailboxes/${USER_ID}/totp`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${admin}` },
      });
      expect(res.status).toBe(200);

      expect(await query(`SELECT 1 FROM mfa_totp WHERE mailbox_id = $1`, [USER_ID])).toHaveLength(0);
      // Codes minted against the old secret are no use against a new one, and
      // leaving them behind is a second set of credentials nobody tracks.
      expect(await query(`SELECT 1 FROM mfa_recovery_codes WHERE mailbox_id = $1`, [USER_ID])).toHaveLength(0);
    });

    // The catch-all DELETE under /mailboxes/ matches this path too. With the
    // routes in the wrong order, pressing "remove second factor" was routed to
    // deleting the mailbox - it only failed because the id came out malformed.
    it("removing a second factor does not delete the user", async () => {
      await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [USER_ID]);
      await query(
        `INSERT INTO mfa_totp (mailbox_id, label, secret_enc, secret_nonce, key_version, status, confirmed_at)
         VALUES ($1, 'test', '\\x00'::bytea, '\\x00'::bytea, 1, 'CONFIRMED', NOW())`,
        [USER_ID],
      );

      const res = await fetch(`${base}/api/v1/admin/mailboxes/${USER_ID}/totp`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${admin}` },
      });
      expect(res.status).toBe(200);

      const still = await query(`SELECT 1 FROM mailboxes WHERE id = $1`, [USER_ID]);
      expect(still).toHaveLength(1);
    });

    it("answers 404 for a user that does not exist rather than leaking the difference", async () => {
      const res = await get(`/api/v1/admin/mailboxes/44444444-4444-4444-4444-444444444444/aliases`, admin);
      expect(res.status).toBe(404);
    });
  });
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

/**
 * What each surface costs to open, and what a session grants once opened.
 *
 * Two rules made the app tiresome in a way no unit test could see. Reading your
 * own mail demanded a second factor, which the mailbox does not require - only
 * the console does. And a session issued at the admin sign-in carried the admin
 * audience alone, so leaving the console landed on the webmail's login screen,
 * asking for a password from somebody who had just proven a password and a
 * second factor a moment earlier.
 */
describeDb("moving between the surfaces", () => {
  const stepUpEmail = () => `stepup@authtest-${DOMAIN_ID}.invalid`;
  const plainEmail = () => `plainuser@authtest-${DOMAIN_ID}.invalid`;
  let stepUpId = "";

  /** Reads the Set-Cookie headers, which is where the two sessions land. */
  async function postForCookies(path: string, body: unknown, token?: string) {
    const res = await fetch(`${base}${path}`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify(body),
    });
    return {
      status: res.status,
      cookies: res.headers.getSetCookie(),
      body: (await res.json().catch(() => ({}))) as Record<string, never>,
    };
  }

  const cookieNames = (cookies: string[]) => cookies.map((c) => c.split("=")[0]).sort();

  /**
   * One session per account, taken once.
   *
   * Every login costs an Argon2 verify, and this service deliberately bounds
   * how many of those run at a time, so a test file that signs in per assertion
   * spends its time queueing behind itself. A session does not expire during
   * the run, so reusing it tests the same thing and leaves the budget for the
   * assertions that need a fresh sign-in.
   */
  let stepUpToken = "";
  let plainToken = "";
  let plainId = "";

  beforeAll(async () => {
    await startServer();
    await seed();
    const hash = await hashPassword(PASSWORD);
    for (const [local, role] of [["stepup", "SUPER_ADMIN"], ["plainuser", "USER"]] as const) {
      await query(
        `INSERT INTO mailboxes (domain_id, local_part, email_address, password_hash, role)
         VALUES ($1, $2, $3, $4, $5) ON CONFLICT (domain_id, local_part) DO NOTHING`,
        [DOMAIN_ID, local, `${local}@authtest-${DOMAIN_ID}.invalid`, hash, role],
      );
    }
    stepUpId = (await query<{ id: string }>(
      `SELECT id FROM mailboxes WHERE email_address = $1`, [stepUpEmail()]))[0].id;
    plainId = (await query<{ id: string }>(
      `SELECT id FROM mailboxes WHERE email_address = $1`, [plainEmail()]))[0].id;

    stepUpToken = (await post("/api/v1/auth/user/login",
      { email: stepUpEmail(), password: PASSWORD })).body.token as unknown as string;
    plainToken = (await post("/api/v1/auth/user/login",
      { email: plainEmail(), password: PASSWORD })).body.token as unknown as string;
  }, 120000);

  afterAll(async () => {
    server?.close();
    await query(`DELETE FROM domains WHERE id = $1`, [DOMAIN_ID]).catch(() => undefined);
    await closePool();
  });

  // The mailbox is one account's own mail behind that account's own password.
  // Demanding a code as well meant an enrolled account could not read its mail
  // without its phone.
  it("opens the webmail on the password alone, even with a factor enrolled", async () => {
    await enrollTotp(stepUpId, stepUpToken);

    const login = await post("/api/v1/auth/user/login", { email: stepUpEmail(), password: PASSWORD });
    expect(login.status).toBe(200);
    expect(login.body.mfa_required).toBeUndefined();
    expect(login.body.token).toBeDefined();

    // And the session it hands out is one the API actually accepts.
    expect((await get("/api/v1/user/me", login.body.token)).status).toBe(200);
  }, 120000);

  // A webmail session is still not an admin session; that is the isolation the
  // whole audience split exists for.
  it("does not let the webmail session open the console by itself", async () => {
    expect((await get("/api/v1/admin/dashboard", stepUpToken)).status).toBe(401);
  });

  // The sign-in checked the password and the second factor and never the role,
  // so an ordinary account that had enrolled a factor - which any account may
  // do from its own settings - could sign in at /admin/login and be handed an
  // admin-audience session. The step-up checks the role; this path did not.
  it("refuses an ordinary account at the admin sign-in, factor or no factor", async () => {
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [plainId]);
    const secret = await enrollTotp(plainId, plainToken);

    const attempt = await post("/api/v1/auth/admin/login", { email: plainEmail(), password: PASSWORD });
    expect(attempt.status).toBe(403);
    expect(attempt.body.error).toBe("FORBIDDEN");

    // And no challenge came back that could be verified into one anyway.
    expect(attempt.body.challenge_token).toBeUndefined();
    void secret;
  }, 120000);

  it("refuses a step-up without a webmail session behind it", async () => {
    expect((await post("/api/v1/auth/admin/step-up", { code: "123456" })).status).toBe(401);
  });

  it("refuses a step-up with the wrong code, and never on the password alone", async () => {
    const res = await post("/api/v1/auth/admin/step-up", { code: "000000" }, stepUpToken);
    expect(res.status).toBe(401);
    expect(res.body.error).toBe("INVALID_MFA_CODE");
  });

  // The role is read from the database rather than from the token, so an
  // ordinary account cannot cross over however valid its session is.
  it("refuses a step-up from an account that may not open the console", async () => {
    const res = await post("/api/v1/auth/admin/step-up", { code: "123456" }, plainToken);
    expect(res.status).toBe(403);
  });

  // The point of the step-up: the second factor, and not the password, which
  // the session in hand proved already.
  it("crosses into the console on the second factor alone", async () => {
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [stepUpId]);
    const secret = await enrollTotp(stepUpId, stepUpToken);

    const stepUp = await postForCookies(
      "/api/v1/auth/admin/step-up",
      { code: generateHotp(base32Decode(secret), getCurrentStep()) },
      stepUpToken,
    );
    expect(stepUp.status).toBe(200);
    expect((await get("/api/v1/admin/dashboard", stepUp.body.token)).status).toBe(200);

    // Both sessions come back, which is what makes leaving the console free.
    expect(cookieNames(stepUp.cookies)).toEqual(["lm_admin_session", "lm_user_session"]);
  }, 120000);

  // Proving a password and a second factor is strictly more than the webmail
  // asks for, so it must not leave the operator without a webmail session.
  it("issues a webmail session at the admin sign-in too", async () => {
    await query(`DELETE FROM mfa_totp WHERE mailbox_id = $1`, [stepUpId]);
    const secret = await enrollTotp(stepUpId, stepUpToken);

    const challenge = await post("/api/v1/auth/admin/login", { email: stepUpEmail(), password: PASSWORD });
    expect(challenge.body.mfa_required).toBe(true);

    const verified = await postForCookies("/api/v1/auth/mfa/verify", {
      challenge_token: challenge.body.challenge_token,
      code: generateHotp(base32Decode(secret), getCurrentStep()),
    });
    expect(verified.status).toBe(200);
    expect(cookieNames(verified.cookies)).toEqual(["lm_admin_session", "lm_user_session"]);

    // The webmail cookie has to be a webmail token, not the admin one copied
    // across: an admin-audience token is refused by every /user route.
    const userCookie = verified.cookies.find((c) => c.startsWith("lm_user_session="))!;
    const userToken = userCookie.slice("lm_user_session=".length).split(";")[0];
    expect((await get("/api/v1/user/me", userToken)).status).toBe(200);
  }, 120000);

  // Signing in twice in the same second produced a byte-identical token, which
  // collided with the unique index on web_sessions.refresh_token_hash and
  // answered 500 to a correct password. A double-clicked sign-in was enough.
  it("survives two sign-ins in the same second", async () => {
    const [first, second] = await Promise.all([
      post("/api/v1/auth/user/login", { email: plainEmail(), password: PASSWORD }),
      post("/api/v1/auth/user/login", { email: plainEmail(), password: PASSWORD }),
    ]);
    expect(first.status).toBe(200);
    expect(second.status).toBe(200);
    expect(first.body.token).not.toBe(second.body.token);
  }, 60000);
});

if (!hasDatabase) {
  // eslint-disable-next-line no-console
  console.warn("TEST_DATABASE_URL not set - auth API integration tests skipped");
}

// generateTotpSecret is exercised through the enrollment flow above; this
// keeps the import meaningful when the database suite is skipped.
void generateTotpSecret;

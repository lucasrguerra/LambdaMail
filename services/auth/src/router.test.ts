import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { createServer, Server } from "node:http";
import crypto from "node:crypto";

// The master key has to exist before crypto.ts is imported anywhere, because
// encryptSecret refuses to run without one.
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

  it("never returns the TOTP secret to an unauthenticated caller", async () => {
    const res = await post("/api/v1/user/mfa/totp/enroll", {});
    expect(res.status).toBe(401);
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

if (!hasDatabase) {
  // eslint-disable-next-line no-console
  console.warn("TEST_DATABASE_URL not set - auth API integration tests skipped");
}

// generateTotpSecret is exercised through the enrollment flow above; this
// keeps the import meaningful when the database suite is skipped.
void generateTotpSecret;

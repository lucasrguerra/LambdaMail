import { describe, it, expect } from "vitest";
import { hashPassword, verifyPassword, encryptSecret, decryptSecret } from "./crypto.js";
import { generateTotpSecret, verifyTotpCode, generateHotp, base32Decode, getCurrentStep } from "./totp.js";
import { generateRecoveryCodes, generateAppPassword, hashAppPassword, verifyAppPassword } from "./mfaHelpers.js";
import { createJwt, verifyJwt, isSurfaceAuthorized, isStepUpMfaRequired } from "./session.js";

describe("crypto module", () => {
  it("hashes and verifies passwords correctly", async () => {
    const password = "SecretPassword123!";
    const hash = await hashPassword(password);
    expect(hash.startsWith("$argon2id$")).toBe(true);
    expect(await verifyPassword(password, hash)).toBe(true);
    expect(await verifyPassword("WrongPassword", hash)).toBe(false);
  });

  // The Go service (alexedwards/argon2id) writes the hashes this service has
  // to accept, and vice versa. A mismatch means a password set through the
  // mail stack cannot be used to log into the webmail.
  it("verifies an Argon2id hash produced by the Go service", async () => {
    const goHash = "$argon2id$v=19$m=65536,t=1,p=16$lw9+BftyPYW4ILsZu9ZAPw$ueO26QNhturq/vuNMlEgVZhXTZwQi+tPYeUFjaeRdIs";
    expect(await verifyPassword("dev-password-only", goHash)).toBe(true);
    expect(await verifyPassword("wrong", goHash)).toBe(false);
  });

  // Regression: verifyPassword used to fall back to comparing the password
  // against the stored hash, so anyone holding a database dump could
  // authenticate by sending the hash back as the password.
  it("refuses to accept the stored hash itself as the password", async () => {
    const hash = await hashPassword("RealPassword123!");
    expect(await verifyPassword(hash, hash)).toBe(false);
  });

  it("rejects any hash that is not argon2id, instead of guessing a scheme", async () => {
    const sha256OfPassword = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8";
    expect(await verifyPassword("password", sha256OfPassword)).toBe(false);
    expect(await verifyPassword("password", "pbkdf2$sha512$100000$abc$def")).toBe(false);
    expect(await verifyPassword("password", "")).toBe(false);
  });

  it("encrypts and decrypts secrets using AES-256-GCM", () => {
    process.env.LAMBDAMAIL_MASTER_KEY = "test-master-key-32-chars-long!!";
    const secret = "JBSWY3DPEHPK3PXP";
    const encrypted = encryptSecret(secret);
    const decrypted = decryptSecret(encrypted.encrypted, encrypted.nonce, encrypted.keyVersion);
    expect(decrypted).toBe(secret);
  });
});

describe("totp module", () => {
  it("generates valid base32 secret and uri", () => {
    const res = generateTotpSecret("user@domain.com");
    expect(res.base32Secret).toBeDefined();
    expect(res.uri).toContain("otpauth://totp/LambdaMail:user%40domain.com");
  });

  it("verifies valid code and enforces anti-replay step check", () => {
    const { base32Secret } = generateTotpSecret("test@domain.com");
    const secretBuffer = base32Decode(base32Secret);
    const currentStep = getCurrentStep();
    const validCode = generateHotp(secretBuffer, currentStep);

    // Initial validation
    const result1 = verifyTotpCode(base32Secret, validCode, null);
    expect(result1.valid).toBe(true);
    expect(result1.step).toBe(currentStep);

    // Anti-replay: same step cannot be reused
    const result2 = verifyTotpCode(base32Secret, validCode, result1.step);
    expect(result2.valid).toBe(false);
  });

  it("rejects invalid code format and wrong code", () => {
    const { base32Secret } = generateTotpSecret("test@domain.com");
    expect(verifyTotpCode(base32Secret, "12345").valid).toBe(false);
    expect(verifyTotpCode(base32Secret, "000000").valid).toBe(false);
  });
});

describe("mfaHelpers module", () => {
  it("generates 10 recovery codes and hashes them", async () => {
    const { rawCodes, hashedCodes } = await generateRecoveryCodes(10);
    expect(rawCodes.length).toBe(10);
    expect(hashedCodes.length).toBe(10);
    expect(await verifyPassword(rawCodes[0], hashedCodes[0])).toBe(true);
    expect(hashedCodes.every((h) => h.startsWith("$argon2id$"))).toBe(true);
  });

  it("generates app passwords with prefix", async () => {
    const appPass = generateAppPassword();
    expect(appPass.startsWith("lmp_")).toBe(true);
    const hash = await hashAppPassword(appPass);
    expect(await verifyAppPassword(appPass, hash)).toBe(true);
  });
});

describe("surface isolation and session module", () => {
  it("strictly rejects user token when presented to admin surface", () => {
    const userToken = createJwt({
      sub: "mailbox-123",
      email: "admin@domain.com",
      role: "SUPER_ADMIN", // Even with SUPER_ADMIN role!
      domainId: "domain-123",
      surface: "user",
      aud: "lambdamail:user",
      mfaSatisfied: false,
      purpose: "session",
    });

    const parsed = verifyJwt(userToken);
    expect(parsed).not.toBeNull();
    // Rejection check:
    expect(isSurfaceAuthorized(parsed, "admin")).toBe(false);
    expect(isSurfaceAuthorized(parsed, "user")).toBe(true);
  });

  it("accepts valid admin token with mfaSatisfied=true", () => {
    const adminToken = createJwt({
      sub: "mailbox-123",
      email: "admin@domain.com",
      role: "SUPER_ADMIN",
      domainId: "domain-123",
      surface: "admin",
      aud: "lambdamail:admin",
      mfaSatisfied: true,
      mfaSatisfiedAt: Date.now(),
      purpose: "session",
    });

    const parsed = verifyJwt(adminToken);
    expect(parsed).not.toBeNull();
    expect(isSurfaceAuthorized(parsed, "admin")).toBe(true);
    expect(isStepUpMfaRequired(parsed!, 15)).toBe(false);
  });

  it("requires step-up MFA if mfaSatisfiedAt is older than 15 minutes", () => {
    const oldTimestamp = Date.now() - 20 * 60 * 1000; // 20 mins ago
    const adminToken = createJwt({
      sub: "mailbox-123",
      email: "admin@domain.com",
      role: "SUPER_ADMIN",
      domainId: "domain-123",
      surface: "admin",
      aud: "lambdamail:admin",
      mfaSatisfied: true,
      mfaSatisfiedAt: oldTimestamp,
      purpose: "session",
    });

    const parsed = verifyJwt(adminToken)!;
    expect(isStepUpMfaRequired(parsed, 15)).toBe(true);
  });
});

// A challenge token is issued after the password but before the second
// factor. Treating it as a session would skip MFA entirely.
describe("challenge tokens are not sessions", () => {
  it("refuses a challenge token wherever a session is required", () => {
    const challenge = createJwt({
      sub: "mailbox-123",
      email: "admin@domain.com",
      role: "SUPER_ADMIN",
      domainId: "domain-123",
      surface: "admin",
      aud: "lambdamail:admin",
      mfaSatisfied: true,
      mfaSatisfiedAt: Date.now(),
      purpose: "mfa_challenge",
    });

    const parsed = verifyJwt(challenge);
    expect(parsed).not.toBeNull();
    expect(isSurfaceAuthorized(parsed, "admin")).toBe(false);
    expect(isSurfaceAuthorized(parsed, "user")).toBe(false);
  });
});

import { describe, it, expect } from "vitest";
import { hashPassword, verifyPassword, encryptSecret, decryptSecret } from "./crypto.js";
import { generateTotpSecret, verifyTotpCode, generateHotp, base32Decode, getCurrentStep } from "./totp.js";
import { generateRecoveryCodes, generateAppPassword, hashAppPassword, verifyAppPassword } from "./mfaHelpers.js";
import { createJwt, verifyJwt, isSurfaceAuthorized, isStepUpMfaRequired } from "./session.js";

describe("crypto module", () => {
  it("hashes and verifies passwords correctly", () => {
    const password = "SecretPassword123!";
    const hash = hashPassword(password);
    expect(verifyPassword(password, hash)).toBe(true);
    expect(verifyPassword("WrongPassword", hash)).toBe(false);
  });

  it("encrypts and decrypts secrets using AES-256-GCM", () => {
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
  it("generates 10 recovery codes and hashes them", () => {
    const { rawCodes, hashedCodes } = generateRecoveryCodes(10);
    expect(rawCodes.length).toBe(10);
    expect(hashedCodes.length).toBe(10);
    expect(verifyPassword(rawCodes[0], hashedCodes[0])).toBe(true);
  });

  it("generates app passwords with prefix", () => {
    const appPass = generateAppPassword();
    expect(appPass.startsWith("lmp_")).toBe(true);
    const hash = hashAppPassword(appPass);
    expect(verifyAppPassword(appPass, hash)).toBe(true);
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
    });

    const parsed = verifyJwt(adminToken)!;
    expect(isStepUpMfaRequired(parsed, 15)).toBe(true);
  });
});

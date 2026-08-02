import crypto from "node:crypto";

export interface SessionTokenPayload {
  sub: string; // mailbox_id
  email: string;
  role: "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN";
  domainId: string;
  surface: "user" | "admin";
  aud: "lambdamail:user" | "lambdamail:admin";
  mfaSatisfied: boolean;
  mfaSatisfiedAt?: number;
  // Separates the short-lived token handed out between password and second
  // factor from a real session. Without it, a challenge token - issued before
  // any second factor was proven - would be accepted anywhere a session is.
  purpose: "mfa_challenge" | "session";
  iat: number;
  exp: number;
}

/**
 * The signing secret. There is deliberately no default.
 *
 * A fallback constant here would be published in this repository, so any
 * deployment that forgot the variable would accept tokens anyone could mint -
 * including an admin session. Refusing to start is the only safe behaviour.
 */
const JWT_SECRET = (() => {
  const secret = process.env.JWT_SECRET;
  if (!secret || secret.length < 32) {
    throw new Error(
      "JWT_SECRET must be set to at least 32 characters; refusing to sign sessions with a guessable key",
    );
  }
  return secret;
})();

function base64UrlEncode(str: string): string {
  return Buffer.from(str)
    .toString("base64")
    .replace(/=/g, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_");
}

function base64UrlDecode(str: string): string {
  let base64 = str.replace(/-/g, "+").replace(/_/g, "/");
  while (base64.length % 4) base64 += "=";
  return Buffer.from(base64, "base64").toString("utf8");
}

export function createJwt(payload: Omit<SessionTokenPayload, "iat" | "exp">, expiresInSeconds = 28800): string {
  const header = { alg: "HS256", typ: "JWT" };
  const now = Math.floor(Date.now() / 1000);
  const fullPayload: SessionTokenPayload = {
    ...payload,
    iat: now,
    exp: now + expiresInSeconds,
  };

  const encodedHeader = base64UrlEncode(JSON.stringify(header));
  const encodedPayload = base64UrlEncode(JSON.stringify(fullPayload));
  const signatureInput = `${encodedHeader}.${encodedPayload}`;

  const signature = crypto
    .createHmac("sha256", JWT_SECRET)
    .update(signatureInput)
    .digest("base64")
    .replace(/=/g, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_");

  return `${signatureInput}.${signature}`;
}

export function verifyJwt(token: string): SessionTokenPayload | null {
  try {
    const parts = token.split(".");
    if (parts.length !== 3) return null;
    const [headerB64, payloadB64, signatureB64] = parts;
    const signatureInput = `${headerB64}.${payloadB64}`;

    const expectedSignature = crypto
      .createHmac("sha256", JWT_SECRET)
      .update(signatureInput)
      .digest("base64")
      .replace(/=/g, "")
      .replace(/\+/g, "-")
      .replace(/\//g, "_");

    if (!crypto.timingSafeEqual(Buffer.from(signatureB64), Buffer.from(expectedSignature))) {
      return null;
    }

    const payload: SessionTokenPayload = JSON.parse(base64UrlDecode(payloadB64));
    const now = Math.floor(Date.now() / 1000);
    if (payload.exp < now) return null;

    return payload;
  } catch {
    return null;
  }
}

export function isSurfaceAuthorized(
  payload: SessionTokenPayload | null,
  requiredSurface: "user" | "admin",
): boolean {
  if (!payload) return false;
  // A challenge token proves only that a password was right. Treating it as a
  // session would let the whole second factor be skipped by presenting it.
  if (payload.purpose !== "session") return false;
  const expectedAud = requiredSurface === "admin" ? "lambdamail:admin" : "lambdamail:user";
  if (payload.aud !== expectedAud || payload.surface !== requiredSurface) {
    return false;
  }
  if (requiredSurface === "admin" && !payload.mfaSatisfied) {
    return false;
  }
  return true;
}

export function isStepUpMfaRequired(payload: SessionTokenPayload, maxAgeMinutes = 15): boolean {
  if (!payload.mfaSatisfiedAt) return true;
  const ageMs = Date.now() - payload.mfaSatisfiedAt;
  return ageMs > maxAgeMinutes * 60 * 1000;
}

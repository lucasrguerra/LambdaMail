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
  iat: number;
  exp: number;
}

const JWT_SECRET = process.env.JWT_SECRET || "lambdamail_jwt_secret_development_only";

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

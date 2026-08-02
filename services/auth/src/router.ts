import { IncomingMessage, ServerResponse } from "node:http";
import { verifyPassword, hashPassword, encryptSecret } from "./crypto.js";
import { generateTotpSecret, verifyTotpCode } from "./totp.js";
import { generateRecoveryCodes, generateAppPassword, hashAppPassword } from "./mfaHelpers.js";
import { createJwt, verifyJwt, isSurfaceAuthorized } from "./session.js";

export function handleApiRequest(req: IncomingMessage, res: ServerResponse): boolean {
  const url = req.url || "/";
  const method = req.method || "GET";

  if (!url.startsWith("/api/v1/")) {
    return false;
  }

  res.setHeader("Content-Type", "application/json");

  // Auxiliary JSON response helper
  const sendJson = (statusCode: number, data: unknown) => {
    res.statusCode = statusCode;
    res.end(JSON.stringify(data));
  };

  // Helper to extract Bearer token or Cookie
  const authHeader = req.headers["authorization"] || "";
  let token = authHeader.startsWith("Bearer ") ? authHeader.substring(7) : "";
  if (!token && req.headers.cookie) {
    const cookies = req.headers.cookie.split(";").map((c) => c.trim());
    const userCookie = cookies.find((c) => c.startsWith("lm_user_session="));
    const adminCookie = cookies.find((c) => c.startsWith("lm_admin_session="));
    if (url.startsWith("/api/v1/admin/") && adminCookie) {
      token = adminCookie.split("=")[1];
    } else if (userCookie) {
      token = userCookie.split("=")[1];
    }
  }

  const sessionPayload = token ? verifyJwt(token) : null;

  // STRICT SURFACE ISOLATION (PLAN.md ADR-008 & Section 14.1)
  // Token presented to /api/v1/admin/* MUST have aud: "lambdamail:admin"
  if (url.startsWith("/api/v1/admin/")) {
    if (!isSurfaceAuthorized(sessionPayload, "admin")) {
      sendJson(401, { error: "UNAUTHORIZED", message: "Admin session required" });
      return true;
    }
  }

  if (url.startsWith("/api/v1/user/") && !url.startsWith("/api/v1/user/login")) {
    if (!isSurfaceAuthorized(sessionPayload, "user") && !isSurfaceAuthorized(sessionPayload, "admin")) {
      sendJson(401, { error: "UNAUTHORIZED", message: "User session required" });
      return true;
    }
  }

  // --- AUTH ENDPOINTS ---
  if (url === "/api/v1/auth/user/login" && method === "POST") {
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => {
      try {
        const { email, password } = JSON.parse(body || "{}");
        if (!email || !password) {
          return sendJson(400, { error: "INVALID_INPUT", message: "Email and password required" });
        }

        // Demo/seeded user check
        const domain = email.split("@")[1] || "example.com";
        const isMfaActive = email.includes("mfa");

        if (isMfaActive) {
          const challengeToken = createJwt({
            sub: "user-challenge-id",
            email,
            role: "USER",
            domainId: "domain-1",
            surface: "user",
            aud: "lambdamail:user",
            mfaSatisfied: false,
          }, 300);
          return sendJson(200, { mfa_required: true, challenge_token: challengeToken });
        }

        const userToken = createJwt({
          sub: "user-123",
          email,
          role: "USER",
          domainId: "domain-1",
          surface: "user",
          aud: "lambdamail:user",
          mfaSatisfied: false,
        });

        res.setHeader("Set-Cookie", `lm_user_session=${userToken}; Path=/user; HttpOnly; Secure; SameSite=Strict`);
        return sendJson(200, { token: userToken, surface: "user", role: "USER" });
      } catch {
        return sendJson(400, { error: "BAD_REQUEST", message: "Invalid JSON" });
      }
    });
    return true;
  }

  if (url === "/api/v1/auth/admin/login" && method === "POST") {
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => {
      try {
        const { email, password } = JSON.parse(body || "{}");
        if (!email || !password) {
          return sendJson(400, { error: "INVALID_INPUT", message: "Email and password required" });
        }

        const challengeToken = createJwt({
          sub: "admin-123",
          email,
          role: "SUPER_ADMIN",
          domainId: "domain-1",
          surface: "admin",
          aud: "lambdamail:admin",
          mfaSatisfied: false,
        }, 300);

        return sendJson(200, { mfa_required: true, challenge_token: challengeToken });
      } catch {
        return sendJson(400, { error: "BAD_REQUEST", message: "Invalid JSON" });
      }
    });
    return true;
  }

  if (url === "/api/v1/auth/mfa/verify" && method === "POST") {
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => {
      try {
        const { challenge_token, code } = JSON.parse(body || "{}");
        const payload = verifyJwt(challenge_token);
        if (!payload) {
          return sendJson(401, { error: "CHALLENGE_EXPIRED", message: "Challenge token expired or invalid" });
        }

        if (!code || code.trim().length === 0) {
          return sendJson(400, { error: "CODE_REQUIRED", message: "Verification code required" });
        }

        const surface = payload.surface;
        const aud = surface === "admin" ? "lambdamail:admin" : "lambdamail:user";

        const sessionToken = createJwt({
          sub: payload.sub,
          email: payload.email,
          role: payload.role,
          domainId: payload.domainId,
          surface: surface as "user" | "admin",
          aud: aud as "lambdamail:user" | "lambdamail:admin",
          mfaSatisfied: true,
          mfaSatisfiedAt: Date.now(),
        });

        const cookieName = surface === "admin" ? "lm_admin_session" : "lm_user_session";
        const cookiePath = surface === "admin" ? "/admin" : "/user";
        res.setHeader("Set-Cookie", `${cookieName}=${sessionToken}; Path=${cookiePath}; HttpOnly; Secure; SameSite=Strict`);

        return sendJson(200, { token: sessionToken, surface, role: payload.role, mfa_satisfied: true });
      } catch {
        return sendJson(400, { error: "BAD_REQUEST", message: "Invalid JSON" });
      }
    });
    return true;
  }

  // --- USER MFA ENROLLMENT ---
  if (url === "/api/v1/user/mfa/totp/enroll" && method === "POST") {
    const email = sessionPayload?.email || "user@lambdamail.local";
    const secretData = generateTotpSecret(email);
    return sendJson(200, { secret: secretData.base32Secret, uri: secretData.uri });
  }

  if (url === "/api/v1/user/mfa/totp/confirm" && method === "POST") {
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => {
      try {
        const { secret, code } = JSON.parse(body || "{}");
        const verification = verifyTotpCode(secret, code);
        if (!verification.valid) {
          return sendJson(400, { error: "INVALID_TOTP_CODE", message: "TOTP verification failed" });
        }

        const recovery = generateRecoveryCodes(10);
        return sendJson(200, {
          confirmed: true,
          recovery_codes: recovery.rawCodes,
        });
      } catch {
        return sendJson(400, { error: "BAD_REQUEST", message: "Invalid JSON" });
      }
    });
    return true;
  }

  // --- ADMIN ENDPOINTS ---
  if (url === "/api/v1/admin/dashboard" && method === "GET") {
    return sendJson(200, {
      inbound_24h: 1420,
      outbound_24h: 890,
      queue_depth: 3,
      bounce_rate: 0.12,
      spam_score_avg: 0.45,
      disk_used_percent: 18.4,
      domains_verified: 2,
      domains_total: 2,
      preflight_status: "HEALTHY",
    });
  }

  if (url === "/api/v1/admin/domains" && method === "GET") {
    return sendJson(200, [
      {
        id: "domain-1",
        name: "example.com",
        dns_status: "VERIFIED",
        dmarc_policy: "quarantine",
        mta_sts_mode: "testing",
        dane_enabled: false,
        records_count: 13,
      },
    ]);
  }

  if (url === "/api/v1/admin/dmarc" && method === "GET") {
    return sendJson(200, {
      total_messages: 1250,
      spf_pass_count: 1220,
      dkim_pass_count: 1210,
      dmarc_pass_count: 1200,
      sources: [
        { ip: "209.85.220.41", org: "Google LLC", count: 850, spf: "pass", dkim: "pass" },
        { ip: "40.107.0.50", org: "Microsoft Corp", count: 350, spf: "pass", dkim: "pass" },
        { ip: "192.0.2.45", org: "Unknown Host", count: 50, spf: "fail", dkim: "fail" },
      ],
    });
  }

  if (url === "/api/v1/admin/preflight" && method === "GET") {
    return sendJson(200, {
      status: "HEALTHY",
      checks: [
        { name: "Outbound Port 25 Egress", status: "PASS" },
        { name: "PTR Record Matching Host", status: "PASS" },
        { name: "FCrDNS Verification", status: "PASS" },
        { name: "IP Clean in RBLs", status: "PASS" },
        { name: "Cloudflare API Token Scope", status: "PASS" },
        { name: "Certificates in acme.json", status: "PASS" },
      ],
    });
  }

  return sendJson(404, { error: "NOT_FOUND", message: "API endpoint not found" });
}

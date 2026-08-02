import { describe, it, expect } from "vitest";
import { createServer, Server } from "node:http";
import { handleApiRequest } from "./router.js";

function startTestServer(): Promise<{ server: Server; url: string }> {
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      if (handleApiRequest(req, res)) return;
      res.statusCode = 404;
      res.end();
    });
    server.listen(0, () => {
      const addr = server.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      resolve({ server, url: `http://localhost:${port}` });
    });
  });
}

describe("API Router & Surface Isolation", () => {
  it("enforces strict surface isolation rejecting user token on admin endpoint", async () => {
    const { server, url } = await startTestServer();
    try {
      // 1. Login as User
      const userLoginRes = await fetch(`${url}/api/v1/auth/user/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: "user@example.com", password: "Password123" }),
      });
      const userLoginData = await userLoginRes.json();
      expect(userLoginRes.status).toBe(200);
      expect(userLoginData.token).toBeDefined();

      // 2. Attempt to access /api/v1/admin/dashboard using user token
      const adminReqRes = await fetch(`${url}/api/v1/admin/dashboard`, {
        headers: { Authorization: `Bearer ${userLoginData.token}` },
      });
      expect(adminReqRes.status).toBe(401);
      const errorData = await adminReqRes.json();
      expect(errorData.error).toBe("UNAUTHORIZED");
    } finally {
      server.close();
    }
  });

  it("allows access to admin endpoint with valid MFA-satisfied admin session token", async () => {
    const { server, url } = await startTestServer();
    try {
      // 1. Login as Admin -> requires MFA challenge
      const adminLoginRes = await fetch(`${url}/api/v1/auth/admin/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: "admin@example.com", password: "AdminPassword123" }),
      });
      const adminLoginData = await adminLoginRes.json();
      expect(adminLoginRes.status).toBe(200);
      expect(adminLoginData.mfa_required).toBe(true);

      // 2. Verify MFA code
      const mfaVerifyRes = await fetch(`${url}/api/v1/auth/mfa/verify`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ challenge_token: adminLoginData.challenge_token, code: "123456" }),
      });
      const mfaVerifyData = await mfaVerifyRes.json();
      expect(mfaVerifyRes.status).toBe(200);
      expect(mfaVerifyData.token).toBeDefined();

      // 3. Access admin dashboard with valid admin token
      const adminDashboardRes = await fetch(`${url}/api/v1/admin/dashboard`, {
        headers: { Authorization: `Bearer ${mfaVerifyData.token}` },
      });
      expect(adminDashboardRes.status).toBe(200);
      const dashboardData = await adminDashboardRes.json();
      expect(dashboardData.preflight_status).toBe("HEALTHY");
    } finally {
      server.close();
    }
  });
});

import { describe, expect, it } from "vitest";
import { buildHealthHandler } from "./health.js";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";

async function withServer(
  handler: ReturnType<typeof buildHealthHandler>,
  run: (baseUrl: string) => Promise<void>,
) {
  const server = createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, resolve));
  const { port } = server.address() as AddressInfo;
  try {
    await run(`http://127.0.0.1:${port}`);
  } finally {
    server.close();
  }
}

describe("buildHealthHandler", () => {
  it("returns 200 on /health/live regardless of dependency state", async () => {
    const handler = buildHealthHandler(
      async () => { throw new Error("db down"); },
      async () => { throw new Error("redis down"); },
    );
    await withServer(handler, async (base) => {
      const res = await fetch(`${base}/health/live`);
      expect(res.status).toBe(200);
    });
  });

  it("returns 200 on /health/ready when both dependencies respond", async () => {
    const handler = buildHealthHandler(async () => {}, async () => {});
    await withServer(handler, async (base) => {
      const res = await fetch(`${base}/health/ready`);
      expect(res.status).toBe(200);
    });
  });

  it("returns 503 on /health/ready when a dependency fails", async () => {
    const handler = buildHealthHandler(
      async () => { throw new Error("db down"); },
      async () => {},
    );
    await withServer(handler, async (base) => {
      const res = await fetch(`${base}/health/ready`);
      expect(res.status).toBe(503);
    });
  });
});

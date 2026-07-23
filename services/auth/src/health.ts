import type { IncomingMessage, ServerResponse } from "node:http";

type PingFn = () => Promise<void>;

/**
 * Builds the /health/live and /health/ready handler shared by the auth and
 * storage services (PLAN.md section 11.5 and the compose healthcheck in section 13).
 */
export function buildHealthHandler(pingDb: PingFn, pingRedis: PingFn) {
  return async (req: IncomingMessage, res: ServerResponse) => {
    if (req.url === "/health/live") {
      res.writeHead(200).end();
      return;
    }
    if (req.url === "/health/ready") {
      try {
        await Promise.all([pingDb(), pingRedis()]);
        res.writeHead(200).end();
      } catch {
        res.writeHead(503).end();
      }
      return;
    }
    res.writeHead(404).end();
  };
}

import { createServer } from "node:http";
import { Client } from "pg";
import { Redis } from "ioredis";
import { buildHealthHandler } from "./health.js";
import { handleApiRequest } from "./router.js";

const port = Number(process.env.PORT ?? 3001);

const healthHandler = buildHealthHandler(
  async () => {
    const client = new Client({ connectionString: process.env.DATABASE_URL });
    await client.connect();
    await client.query("SELECT 1");
    await client.end();
  },
  async () => {
    const redis = new Redis(process.env.REDIS_URL ?? "");
    await redis.ping();
    redis.disconnect();
  },
);

const server = createServer((req, res) => {
  if (handleApiRequest(req, res)) {
    return;
  }
  healthHandler(req, res);
});

server.listen(port, () => {
  console.log(`auth service listening on ${port}`);
});


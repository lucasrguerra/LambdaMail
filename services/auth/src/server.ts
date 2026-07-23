import { createServer } from "node:http";
import { Client } from "pg";
import { Redis } from "ioredis";
import { buildHealthHandler } from "./health.js";

const port = Number(process.env.PORT ?? 3001);

const handler = buildHealthHandler(
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

createServer(handler).listen(port, () => {
  // F0 scaffold: only health endpoints are wired. Auth use cases land in F1+.
  console.log(`auth service listening on ${port}`);
});

import crypto from "node:crypto";
import { hashPassword, verifyPassword } from "./crypto.js";

/**
 * Generates single-use recovery codes. Only the hashes are ever stored, so a
 * database dump cannot be turned back into usable codes (PLAN.md section 9).
 */
export async function generateRecoveryCodes(count = 10): Promise<{ rawCodes: string[]; hashedCodes: string[] }> {
  const rawCodes: string[] = [];
  for (let i = 0; i < count; i++) {
    rawCodes.push(crypto.randomBytes(6).toString("hex").substring(0, 10).toUpperCase());
  }
  // Hashed one at a time, deliberately. Argon2id is configured for 64 MiB per
  // hash, so ten of them in parallel reserve 640 MiB - more than the auth
  // container is allowed - and the process was OOM-killed partway through
  // enrolment. From the browser that surfaced as "upstream service is
  // unreachable"; in the database it left an account with a confirmed second
  // factor and no recovery codes, because the row is written before this runs.
  //
  // Sequentially it peaks at 64 MiB and takes a few seconds, which is the
  // right trade for an operation that happens once per enrolment.
  const hashedCodes: string[] = [];
  for (const code of rawCodes) {
    hashedCodes.push(await hashPassword(code));
  }
  return { rawCodes, hashedCodes };
}

export function generateAppPassword(): string {
  const part = () => crypto.randomBytes(4).toString("hex");
  return `lmp_${part()}-${part()}-${part()}`;
}

export async function hashAppPassword(rawPassword: string): Promise<string> {
  return hashPassword(rawPassword);
}

export async function verifyAppPassword(rawPassword: string, storedHash: string): Promise<boolean> {
  return verifyPassword(rawPassword, storedHash);
}

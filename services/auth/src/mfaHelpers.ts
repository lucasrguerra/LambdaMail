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
  const hashedCodes = await Promise.all(rawCodes.map((c) => hashPassword(c)));
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

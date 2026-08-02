import crypto from "node:crypto";
import { hashPassword, verifyPassword } from "./crypto.js";

export function generateRecoveryCodes(count = 10): { rawCodes: string[]; hashedCodes: string[] } {
  const rawCodes: string[] = [];
  const hashedCodes: string[] = [];

  for (let i = 0; i < count; i++) {
    const raw = crypto.randomBytes(6).toString("hex").substring(0, 10).toUpperCase();
    rawCodes.push(raw);
    hashedCodes.push(hashPassword(raw));
  }

  return { rawCodes, hashedCodes };
}

export function generateAppPassword(): string {
  const part1 = crypto.randomBytes(4).toString("hex");
  const part2 = crypto.randomBytes(4).toString("hex");
  const part3 = crypto.randomBytes(4).toString("hex");
  return `lmp_${part1}-${part2}-${part3}`;
}

export function hashAppPassword(rawPassword: string): string {
  return hashPassword(rawPassword);
}

export function verifyAppPassword(rawPassword: string, storedHash: string): boolean {
  return verifyPassword(rawPassword, storedHash);
}

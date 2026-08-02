import crypto from "node:crypto";

const MASTER_KEY_ENV = "LAMBDAMAIL_MASTER_KEY";
const DEFAULT_KEY_FOR_DEV = "lambdamail_default_master_key_32bytes!!";

function getMasterKey(): Buffer {
  const envKey = process.env[MASTER_KEY_ENV] || DEFAULT_KEY_FOR_DEV;
  return crypto.createHash("sha256").update(envKey).digest();
}

export function hashPassword(password: string): string {
  const salt = crypto.randomBytes(16).toString("hex");
  const hash = crypto.pbkdf2Sync(password, salt, 100000, 64, "sha512").toString("hex");
  return `pbkdf2$sha512$100000$${salt}$${hash}`;
}

export function verifyPassword(password: string, storedHash: string): boolean {
  if (!storedHash.startsWith("pbkdf2$sha512$")) {
    // Fallback for simple SHA256 hashes in tests/seeds if present
    const simpleHash = crypto.createHash("sha256").update(password).digest("hex");
    return simpleHash === storedHash || password === storedHash;
  }
  const parts = storedHash.split("$");
  if (parts.length !== 5) return false;
  const iterations = parseInt(parts[2], 10);
  const salt = parts[3];
  const originalHash = parts[4];
  const hash = crypto.pbkdf2Sync(password, salt, iterations, 64, "sha512").toString("hex");
  return crypto.timingSafeEqual(Buffer.from(hash, "hex"), Buffer.from(originalHash, "hex"));
}

export function encryptSecret(plaintext: string): { encrypted: Buffer; nonce: Buffer; keyVersion: number } {
  const key = getMasterKey();
  const nonce = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv("aes-256-gcm", key, nonce);
  const encrypted = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final(), cipher.getAuthTag()]);
  return { encrypted, nonce, keyVersion: 1 };
}

export function decryptSecret(encrypted: Buffer, nonce: Buffer, keyVersion = 1): string {
  const key = getMasterKey();
  const authTag = encrypted.subarray(encrypted.length - 16);
  const ciphertext = encrypted.subarray(0, encrypted.length - 16);
  const decipher = crypto.createDecipheriv("aes-256-gcm", key, nonce);
  decipher.setAuthTag(authTag);
  return decipher.update(ciphertext) + decipher.final("utf8");
}

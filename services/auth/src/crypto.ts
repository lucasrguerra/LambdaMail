import crypto from "node:crypto";
import { argon2id, argon2Verify } from "hash-wasm";

const MASTER_KEY_ENV = "LAMBDAMAIL_MASTER_KEY";

// Argon2id parameters. These match what the Go side (alexedwards/argon2id)
// and PLAN.md section 9 expect, so a password set through the mail stack
// verifies here and vice versa - both produce and consume the same PHC string.
const ARGON2_MEMORY_KIB = 65536;
const ARGON2_ITERATIONS = 3;
const ARGON2_PARALLELISM = 4;
const ARGON2_HASH_LENGTH = 32;
const ARGON2_SALT_BYTES = 16;

/**
 * Derives the key-encryption key. Unlike the previous implementation there is
 * no development fallback: a missing master key silently encrypting every TOTP
 * secret under a value published in the source tree is worse than refusing to
 * start (PLAN.md risk R9).
 */
function getMasterKey(): Buffer {
  const envKey = process.env[MASTER_KEY_ENV];
  if (!envKey || envKey.length < 16) {
    throw new Error(
      `${MASTER_KEY_ENV} must be set to at least 16 characters; refusing to encrypt secrets under a default key`,
    );
  }
  return crypto.createHash("sha256").update(envKey).digest();
}

/**
 * Caps how many Argon2 operations run at once.
 *
 * Each one reserves ARGON2_MEMORY_KIB - 64 MiB - for its whole duration, and
 * nothing else bounds how many are in flight: every login attempt starts one,
 * from an unauthenticated request. Twenty concurrent sign-ins would reserve
 * 1.28 GiB against a container allowed 512 MiB, so anyone could stop the auth
 * service by opening enough connections. Account lockout does not help; it is
 * per account, and these need not share one.
 *
 * Queuing instead of refusing keeps every request correct, just slower under
 * load, and holds peak memory at CONCURRENCY x 64 MiB no matter the traffic.
 */
const ARGON2_CONCURRENCY = 4;

let active = 0;
const waiting: Array<() => void> = [];

async function withHashSlot<T>(work: () => Promise<T>): Promise<T> {
  if (active >= ARGON2_CONCURRENCY) {
    await new Promise<void>((resolve) => waiting.push(resolve));
  }
  active++;
  try {
    return await work();
  } finally {
    active--;
    // Waking exactly one keeps the count accurate; waking all would let every
    // queued caller through at once, which is the thing being prevented.
    waiting.shift()?.();
  }
}

/** Produces an Argon2id PHC string, the only format this service stores. */
export async function hashPassword(password: string): Promise<string> {
  return withHashSlot(() => argon2id({
    password,
    salt: crypto.randomBytes(ARGON2_SALT_BYTES),
    parallelism: ARGON2_PARALLELISM,
    iterations: ARGON2_ITERATIONS,
    memorySize: ARGON2_MEMORY_KIB,
    hashLength: ARGON2_HASH_LENGTH,
    outputType: "encoded",
  }));
}

/**
 * Verifies a password against an Argon2id PHC string.
 *
 * Anything that is not an argon2id PHC string is rejected outright. The
 * previous implementation fell back to comparing the password against the
 * stored hash itself, which turned every stored hash into a usable credential:
 * anyone holding a database dump could authenticate by sending the hash back.
 * There is no format to be lenient about here - the schema stores exactly one.
 */
export async function verifyPassword(password: string, storedHash: string): Promise<boolean> {
  if (!storedHash || !storedHash.startsWith("$argon2id$")) {
    return false;
  }
  try {
    // Gated as well as hashing, and this is the one that matters most: an
    // unauthenticated login reaches it, so without the cap anyone could
    // reserve 64 MiB per request until the container is killed.
    return await withHashSlot(() => argon2Verify({ password, hash: storedHash }));
  } catch {
    // A malformed stored hash is a failed login, not a crash that would take
    // the login endpoint down for everyone.
    return false;
  }
}

export function encryptSecret(plaintext: string): { encrypted: Buffer; nonce: Buffer; keyVersion: number } {
  const key = getMasterKey();
  const nonce = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv("aes-256-gcm", key, nonce);
  const encrypted = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final(), cipher.getAuthTag()]);
  return { encrypted, nonce, keyVersion: 1 };
}

export function decryptSecret(encrypted: Buffer, nonce: Buffer, _keyVersion = 1): string {
  const key = getMasterKey();
  const authTag = encrypted.subarray(encrypted.length - 16);
  const ciphertext = encrypted.subarray(0, encrypted.length - 16);
  const decipher = crypto.createDecipheriv("aes-256-gcm", key, nonce);
  decipher.setAuthTag(authTag);
  // Concatenated as buffers before decoding: decoding each chunk separately
  // would corrupt any multi-byte character that straddles the boundary.
  return Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString("utf8");
}

/** Constant-time comparison of two short strings of possibly different length. */
export function safeEqual(a: string, b: string): boolean {
  const bufA = Buffer.from(a);
  const bufB = Buffer.from(b);
  if (bufA.length !== bufB.length) return false;
  return crypto.timingSafeEqual(bufA, bufB);
}

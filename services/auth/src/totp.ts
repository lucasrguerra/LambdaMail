import crypto from "node:crypto";

const ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

export function base32Encode(buffer: Buffer): string {
  let bits = 0;
  let value = 0;
  let output = "";
  for (let i = 0; i < buffer.length; i++) {
    value = (value << 8) | buffer[i];
    bits += 8;
    while (bits >= 5) {
      output += ALPHABET[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) {
    output += ALPHABET[(value << (5 - bits)) & 31];
  }
  return output;
}

export function base32Decode(input: string): Buffer {
  const clean = input.toUpperCase().replace(/=+$/, "").replace(/[^A-Z2-7]/g, "");
  let bits = 0;
  let value = 0;
  const bytes: number[] = [];
  for (let i = 0; i < clean.length; i++) {
    const idx = ALPHABET.indexOf(clean[i]);
    if (idx === -1) continue;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      bytes.push((value >>> (bits - 8)) & 255);
      bits -= 8;
    }
  }
  return Buffer.from(bytes);
}

export function getCurrentStep(timeMs: number = Date.now(), periodSeconds = 30): number {
  return Math.floor(timeMs / 1000 / periodSeconds);
}

export function generateHotp(secretBuffer: Buffer, counter: number): string {
  const buffer = Buffer.alloc(8);
  let tmp = counter;
  for (let i = 7; i >= 0; i--) {
    buffer[i] = tmp & 0xff;
    tmp = Math.floor(tmp / 256);
  }

  const hmac = crypto.createHmac("sha1", secretBuffer).update(buffer).digest();
  const offset = hmac[hmac.length - 1] & 0x0f;
  const codeInt =
    ((hmac[offset] & 0x7f) << 24) |
    ((hmac[offset + 1] & 0xff) << 16) |
    ((hmac[offset + 2] & 0xff) << 8) |
    (hmac[offset + 3] & 0xff);

  const code = (codeInt % 1000000).toString().padStart(6, "0");
  return code;
}

export function generateTotpSecret(label: string, issuer = "LambdaMail"): { base32Secret: string; uri: string } {
  const secretBytes = crypto.randomBytes(20);
  const base32Secret = base32Encode(secretBytes);
  const encodedLabel = encodeURIComponent(label);
  const encodedIssuer = encodeURIComponent(issuer);
  const uri = `otpauth://totp/${encodedIssuer}:${encodedLabel}?secret=${base32Secret}&issuer=${encodedIssuer}&algorithm=SHA1&digits=6&period=30`;
  return { base32Secret, uri };
}

export function verifyTotpCode(
  base32Secret: string,
  code: string,
  lastUsedStep: number | null = null,
  timeMs: number = Date.now(),
): { valid: boolean; step: number } {
  const cleanCode = code.trim();
  if (cleanCode.length !== 6 || !/^\d{6}$/.test(cleanCode)) {
    return { valid: false, step: 0 };
  }

  const secretBuffer = base32Decode(base32Secret);
  const currentStep = getCurrentStep(timeMs);

  for (let stepOffset = -1; stepOffset <= 1; stepOffset++) {
    const candidateStep = currentStep + stepOffset;
    if (lastUsedStep !== null && candidateStep <= lastUsedStep) {
      continue;
    }

    const expectedCode = generateHotp(secretBuffer, candidateStep);
    if (crypto.timingSafeEqual(Buffer.from(cleanCode), Buffer.from(expectedCode))) {
      return { valid: true, step: candidateStep };
    }
  }

  return { valid: false, step: 0 };
}

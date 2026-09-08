/**
 * What an uploaded avatar is allowed to be.
 *
 * The type is decided from the bytes, never from the Content-Type the client
 * sent or the name of the file: both are chosen by whoever is uploading, and a
 * stored image is later served back to browsers.
 */

export const MAX_AVATAR_BYTES = 512 * 1024;

export type AvatarType = "image/png" | "image/jpeg" | "image/webp";

export interface Sniffed {
  contentType: AvatarType;
}

const startsWith = (buf: Buffer, sig: number[], offset = 0): boolean =>
  buf.length >= offset + sig.length && sig.every((b, i) => buf[offset + i] === b);

/**
 * Identifies an image from its leading bytes.
 *
 * SVG is deliberately absent. It is a document that can carry script and
 * external references, and serving one back from our own origin would hand an
 * uploader a foothold there; the editor produces a raster image anyway.
 */
export function sniffImage(buf: Buffer): Sniffed | null {
  // \x89PNG\r\n\x1a\n
  if (startsWith(buf, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) {
    return { contentType: "image/png" };
  }
  // JPEG: FF D8 FF
  if (startsWith(buf, [0xff, 0xd8, 0xff])) {
    return { contentType: "image/jpeg" };
  }
  // RIFF....WEBP
  if (startsWith(buf, [0x52, 0x49, 0x46, 0x46]) && startsWith(buf, [0x57, 0x45, 0x42, 0x50], 8)) {
    return { contentType: "image/webp" };
  }
  return null;
}

export type AvatarRejection = "TOO_LARGE" | "EMPTY" | "UNSUPPORTED_TYPE";

export interface AvatarCheck {
  ok: boolean;
  contentType?: AvatarType;
  reason?: AvatarRejection;
}

/** Decides whether these bytes may be stored and later served back. */
export function checkAvatar(buf: Buffer): AvatarCheck {
  if (buf.length === 0) return { ok: false, reason: "EMPTY" };
  // Checked before sniffing, so an enormous upload is refused on its size
  // rather than on its contents.
  if (buf.length > MAX_AVATAR_BYTES) return { ok: false, reason: "TOO_LARGE" };

  const sniffed = sniffImage(buf);
  if (!sniffed) return { ok: false, reason: "UNSUPPORTED_TYPE" };

  return { ok: true, contentType: sniffed.contentType };
}

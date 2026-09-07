import { describe, it, expect } from "vitest";
import { sniffImage, checkAvatar, MAX_AVATAR_BYTES } from "./imageValidation.js";

const png = () => Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0]);
const jpeg = () => Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 0]);
const webp = () => Buffer.concat([Buffer.from("RIFF"), Buffer.from([0, 0, 0, 0]), Buffer.from("WEBPVP8 ")]);

describe("sniffImage", () => {
  it("identifies the formats the editor produces", () => {
    expect(sniffImage(png())?.contentType).toBe("image/png");
    expect(sniffImage(jpeg())?.contentType).toBe("image/jpeg");
    expect(sniffImage(webp())?.contentType).toBe("image/webp");
  });

  // The type comes from the bytes. A client that says "image/png" over an HTML
  // document must not get an HTML document stored and served from our origin.
  it("refuses anything that is not one of them, whatever it claims to be", () => {
    expect(sniffImage(Buffer.from("<!doctype html><script>alert(1)</script>"))).toBeNull();
    expect(sniffImage(Buffer.from("GIF89a"))).toBeNull();
    expect(sniffImage(Buffer.from("%PDF-1.7"))).toBeNull();
  });

  // SVG is an executable document, and it would be served back from our own
  // origin - so it is refused even though it is an image.
  it("refuses SVG", () => {
    expect(sniffImage(Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>'))).toBeNull();
    expect(sniffImage(Buffer.from('<?xml version="1.0"?><svg/>'))).toBeNull();
  });

  it("does not read past the end of a short buffer", () => {
    expect(() => sniffImage(Buffer.from([0x89]))).not.toThrow();
    expect(sniffImage(Buffer.alloc(0))).toBeNull();
    // The first four bytes of a WEBP without the rest is not a WEBP.
    expect(sniffImage(Buffer.from("RIFF"))).toBeNull();
  });
});

describe("checkAvatar", () => {
  it("accepts a real image", () => {
    const res = checkAvatar(png());
    expect(res.ok).toBe(true);
    expect(res.contentType).toBe("image/png");
  });

  it("refuses an empty upload", () => {
    expect(checkAvatar(Buffer.alloc(0))).toMatchObject({ ok: false, reason: "EMPTY" });
  });

  // On size before contents: an enormous upload should not be sniffed first.
  it("refuses one that is too big", () => {
    const huge = Buffer.concat([png(), Buffer.alloc(MAX_AVATAR_BYTES)]);
    expect(checkAvatar(huge)).toMatchObject({ ok: false, reason: "TOO_LARGE" });
  });

  it("refuses a file that is not an image", () => {
    expect(checkAvatar(Buffer.from("just text"))).toMatchObject({ ok: false, reason: "UNSUPPORTED_TYPE" });
  });
});

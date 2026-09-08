import { describe, it, expect } from "vitest";
import {
  minimumScale,
  scaleRange,
  clampTransform,
  initialTransform,
  sourceRect,
} from "./avatarCrop";

const VIEWPORT = 320;

describe("minimumScale", () => {
  // Cover, not contain. Scaling a wide photo to fit its width leaves the top
  // and bottom of the circle empty.
  it("covers the frame on the short side of a wide image", () => {
    expect(minimumScale({ width: 800, height: 400 }, VIEWPORT)).toBeCloseTo(320 / 400);
  });

  it("covers the frame on the short side of a tall image", () => {
    expect(minimumScale({ width: 400, height: 800 }, VIEWPORT)).toBeCloseTo(320 / 400);
  });

  it("scales a small image up rather than leaving it small", () => {
    expect(minimumScale({ width: 100, height: 100 }, VIEWPORT)).toBeCloseTo(3.2);
  });

  it.each([
    [{ width: 0, height: 100 }],
    [{ width: 100, height: 0 }],
    [{ width: Number.NaN, height: 100 }],
  ])("survives a degenerate image %o", (image) => {
    expect(Number.isFinite(minimumScale(image, VIEWPORT))).toBe(true);
  });
});

describe("clampTransform", () => {
  const image = { width: 800, height: 600 };

  it("refuses to zoom below covering the frame", () => {
    const t = clampTransform(image, VIEWPORT, { scale: 0.01, offsetX: 0, offsetY: 0 });
    expect(t.scale).toBeCloseTo(minimumScale(image, VIEWPORT));
  });

  // Without this, dragging far enough pulls the edge of the photo into the
  // circle and the export carries a transparent wedge.
  it("stops the image at its own edge", () => {
    const scale = minimumScale(image, VIEWPORT);
    const t = clampTransform(image, VIEWPORT, { scale, offsetX: 99999, offsetY: 99999 });

    const overhangX = (image.width * scale - VIEWPORT) / 2;
    expect(t.offsetX).toBeCloseTo(overhangX);
    // At the covering scale the short side has no slack at all, so it cannot
    // move: any movement there would expose a gap.
    expect(t.offsetY).toBeCloseTo(0);
  });

  it("allows movement on both axes once zoomed in", () => {
    const scale = minimumScale(image, VIEWPORT) * 2;
    const t = clampTransform(image, VIEWPORT, { scale, offsetX: 99999, offsetY: 99999 });
    expect(t.offsetX).toBeGreaterThan(0);
    expect(t.offsetY).toBeGreaterThan(0);
  });

  it("caps the zoom so the photo cannot be magnified into mush", () => {
    const { max } = scaleRange(image, VIEWPORT);
    expect(clampTransform(image, VIEWPORT, { scale: 9999, offsetX: 0, offsetY: 0 }).scale).toBeCloseTo(max);
  });
});

describe("sourceRect", () => {
  const image = { width: 800, height: 600 };

  it("is always square, because the frame is", () => {
    const r = sourceRect(image, VIEWPORT, { scale: 1.2, offsetX: 30, offsetY: -20 });
    expect(r.width).toBeCloseTo(r.height);
  });

  // At the covering scale the whole short side is used: nothing of the height
  // is cropped away, and the crop is the full 600px of it.
  it("takes the whole short side at the covering scale", () => {
    const r = sourceRect(image, VIEWPORT, initialTransform(image, VIEWPORT));
    expect(r.height).toBeCloseTo(600);
    expect(r.y).toBeCloseTo(0);
    // Centred horizontally: (800 - 600) / 2.
    expect(r.x).toBeCloseTo(100);
  });

  it("never reads outside the image", () => {
    for (const wanted of [
      { scale: 0.001, offsetX: -9999, offsetY: -9999 },
      { scale: 9999, offsetX: 9999, offsetY: 9999 },
      { scale: 1, offsetX: 0, offsetY: 9999 },
    ]) {
      const r = sourceRect(image, VIEWPORT, wanted);
      expect(r.x).toBeGreaterThanOrEqual(-0.001);
      expect(r.y).toBeGreaterThanOrEqual(-0.001);
      expect(r.x + r.width).toBeLessThanOrEqual(image.width + 0.001);
      expect(r.y + r.height).toBeLessThanOrEqual(image.height + 0.001);
    }
  });

  // Dragging the image right shows what is further left in the source.
  it("moves the crop opposite to the drag", () => {
    const scale = minimumScale(image, VIEWPORT) * 2;
    const left = sourceRect(image, VIEWPORT, { scale, offsetX: 40, offsetY: 0 });
    const centre = sourceRect(image, VIEWPORT, { scale, offsetX: 0, offsetY: 0 });
    expect(left.x).toBeLessThan(centre.x);
  });
});

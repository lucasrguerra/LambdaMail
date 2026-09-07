/**
 * The geometry behind the circular photo editor.
 *
 * The editor shows a fixed square viewport with a circular mask; the image sits
 * behind it, scaled and dragged. Everything here works in viewport pixels and
 * is deliberately free of DOM and canvas, so the awkward parts - the smallest
 * scale that still covers the circle, and clamping so no gap can be dragged
 * into view - are testable on their own.
 */

export interface Size {
  width: number;
  height: number;
}

/** Where the image sits behind the viewport, and how big it is drawn. */
export interface Transform {
  /** Multiplier applied to the image's natural size. */
  scale: number;
  /** Image centre, relative to the viewport centre, in viewport pixels. */
  offsetX: number;
  offsetY: number;
}

/**
 * The smallest scale at which the image still covers the whole viewport.
 *
 * Below this a corner of the frame is empty, and the exported photo would carry
 * a transparent wedge - so this is the floor for the zoom control, not merely a
 * suggestion.
 */
export function minimumScale(image: Size, viewport: number): number {
  // Finite, not merely positive: NaN fails every comparison, so `<= 0` lets it
  // through and it then propagates into the transform and blanks the editor.
  // A NaN dimension is what an image that failed to decode reports.
  if (
    !Number.isFinite(image.width) || !Number.isFinite(image.height) || !Number.isFinite(viewport) ||
    image.width <= 0 || image.height <= 0 || viewport <= 0
  ) {
    return 1;
  }
  // Cover, not contain: the larger of the two ratios.
  return Math.max(viewport / image.width, viewport / image.height);
}

/** The zoom control's range: from covering the frame to four times that. */
export function scaleRange(image: Size, viewport: number): { min: number; max: number } {
  const min = minimumScale(image, viewport);
  return { min, max: min * 4 };
}

/**
 * Holds the image over the viewport, whatever was asked for.
 *
 * A drag or a zoom out can otherwise pull an edge inside the frame. Clamping
 * the offset to the overhang keeps the frame covered without fighting the
 * gesture: the image stops at its edge instead of springing back.
 */
export function clampTransform(image: Size, viewport: number, wanted: Transform): Transform {
  const { min, max } = scaleRange(image, viewport);
  const scale = Math.min(Math.max(wanted.scale, min), max);

  // Half of what sticks out past the viewport on each axis.
  const overhangX = Math.max(0, (image.width * scale - viewport) / 2);
  const overhangY = Math.max(0, (image.height * scale - viewport) / 2);

  return {
    scale,
    offsetX: Math.min(Math.max(wanted.offsetX, -overhangX), overhangX),
    offsetY: Math.min(Math.max(wanted.offsetY, -overhangY), overhangY),
  };
}

/** The transform that centres the image at the smallest covering scale. */
export function initialTransform(image: Size, viewport: number): Transform {
  return { scale: minimumScale(image, viewport), offsetX: 0, offsetY: 0 };
}

/**
 * The rectangle of the *source* image that the viewport is showing.
 *
 * This is what the export draws. Working back to source pixels rather than
 * scaling the preview means the saved photo is as sharp as the original
 * allows, instead of being limited to the size of the editor on screen.
 */
export interface SourceRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export function sourceRect(image: Size, viewport: number, transform: Transform): SourceRect {
  const t = clampTransform(image, viewport, transform);
  // One viewport pixel is this many source pixels.
  const perPixel = 1 / t.scale;
  const size = viewport * perPixel;

  // The viewport centre, expressed in source coordinates.
  const centreX = image.width / 2 - t.offsetX * perPixel;
  const centreY = image.height / 2 - t.offsetY * perPixel;

  return {
    x: centreX - size / 2,
    y: centreY - size / 2,
    width: size,
    height: size,
  };
}

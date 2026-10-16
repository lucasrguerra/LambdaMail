/**
 * Turns an uploaded PNG or SVG into the logo BIMI publishes.
 *
 * BIMI accepts one kind of file: SVG Tiny Portable/Secure, square, at most
 * 32 KB, with no script, no raster image inside and nothing fetched from
 * elsewhere. A receiver that is handed anything else shows no logo and says
 * nothing, so the conversion is done here, before upload, and the server
 * checks the result again (protocols/internal/domain/bimi).
 *
 * Three steps, each a pure function:
 *   - sanitizeSvg: an uploaded SVG, reduced to what the format allows
 *   - traceRaster: a PNG, vectorised (a logo cannot carry a bitmap)
 *   - buildLogoSvg: either one, placed on a square canvas the way the person
 *     positioned it, as the final document
 */

/** The ceiling the specification sets for the file. */
export const MAX_LOGO_BYTES = 32 * 1024;

/** The square the logo is drawn on. Only the ratio matters to a receiver. */
export const CANVAS = 512;

/** How much of the square the logo fills when it is first placed. */
const FILL = 0.8;

export type LogoErrorCode = "notXml" | "notSvg" | "noSize" | "empty" | "tooComplex";

export class BimiLogoError extends Error {
  constructor(readonly code: LogoErrorCode) {
    super(code);
  }
}

/** Artwork reduced to drawing elements, with the size it was drawn at. */
export interface Artwork {
  /** The drawing elements, serialised, without any <svg> wrapper. */
  inner: string;
  /** Where the drawing starts and how large it is, in its own units. */
  minX: number;
  minY: number;
  width: number;
  height: number;
  /** Things that were dropped, for the person to know about. */
  removed: string[];
  /** Things kept that may not look the same everywhere ("text": it depends on fonts). */
  warnings: string[];
}

// ------------------------------------------------------------ sanitizing

/** Elements kept: shapes, groups and what gradients need. */
const ALLOWED_ELEMENTS = new Set([
  "g", "path", "rect", "circle", "ellipse", "line", "polyline", "polygon",
  "defs", "lineargradient", "radialgradient", "stop", "text", "tspan", "title", "desc",
]);

/** Dropped without comment: editor noise that draws nothing. */
const SILENT_ELEMENTS = new Set(["metadata", "namedview", "title", "desc"]);

/** Style declarations that become presentation attributes; the rest are dropped. */
const STYLE_PROPERTIES = new Set([
  "fill", "fill-opacity", "fill-rule", "stroke", "stroke-width", "stroke-opacity",
  "stroke-linecap", "stroke-linejoin", "stroke-miterlimit", "opacity", "stop-color", "stop-opacity",
]);

const KEPT_ATTRIBUTES = new Set([
  "d", "x", "y", "cx", "cy", "r", "rx", "ry", "x1", "y1", "x2", "y2", "width", "height", "points",
  "transform", "id", "offset", "gradientunits", "gradienttransform", "spreadmethod", "fx", "fy",
  "font-family", "font-size", "font-weight", "text-anchor", "dx", "dy",
  ...STYLE_PROPERTIES,
]);

function localName(node: Element): string {
  return node.localName.toLowerCase();
}

/** Reads "fill:#000;stroke:none" into presentation attributes. */
function applyStyle(el: Element): void {
  const style = el.getAttribute("style");
  el.removeAttribute("style");
  if (!style) return;
  for (const declaration of style.split(";")) {
    const [rawName, ...rest] = declaration.split(":");
    const name = rawName?.trim().toLowerCase();
    const value = rest.join(":").trim();
    // A url() in a style value would fetch something; only plain values pass
    if (name && STYLE_PROPERTIES.has(name) && value && !/url\s*\(\s*(?!#)/i.test(value)) {
      el.setAttribute(name, value);
    }
  }
}

function cleanElement(el: Element, removed: Set<string>): void {
  applyStyle(el);

  for (const attr of Array.from(el.attributes)) {
    const name = attr.name.toLowerCase();
    // Event handlers, classes, editor namespaces (inkscape:, sodipodi:), links
    // out of the document: none of them draw anything, and some are active
    if (!KEPT_ATTRIBUTES.has(name)) {
      el.removeAttribute(attr.name);
      continue;
    }
    // fill="url(#id)" is how a gradient is used; anything else with url() is out
    if (/url\s*\(\s*(?!#)/i.test(attr.value)) el.removeAttribute(attr.name);
  }

  for (const child of Array.from(el.childNodes)) {
    if (child.nodeType === 1) {
      const element = child as Element;
      const name = localName(element);
      if (!ALLOWED_ELEMENTS.has(name)) {
        if (!SILENT_ELEMENTS.has(name) && !name.includes(":")) removed.add(name);
        el.removeChild(element);
        continue;
      }
      // Titles and descriptions inside the drawing are dropped; the logo gets one title of its own
      if (name === "title" || name === "desc") {
        el.removeChild(element);
        continue;
      }
      cleanElement(element, removed);
    } else if (child.nodeType !== 3) {
      // Comments, processing instructions, CDATA
      el.removeChild(child);
    }
  }
}

/**
 * An anchor wraps drawing: keep what is inside it, lose the link. Done before
 * the cleaning pass, so what comes out of an anchor is cleaned like the rest.
 */
function unwrapAnchors(root: Element, removed: Set<string>): void {
  for (const anchor of Array.from(root.getElementsByTagName("a"))) {
    removed.add("a (link)");
    const parent = anchor.parentNode;
    if (!parent) continue;
    while (anchor.firstChild) parent.insertBefore(anchor.firstChild, anchor);
    parent.removeChild(anchor);
  }
}

function numberOf(value: string | null): number | null {
  if (!value) return null;
  const n = parseFloat(value);
  return Number.isFinite(n) && n > 0 ? n : null;
}

/**
 * Reduces an uploaded SVG to its drawing. Throws BimiLogoError when it is not
 * something that can be placed (not XML, not SVG, no size to place it by).
 */
export function sanitizeSvg(source: string): Artwork {
  const doc = new DOMParser().parseFromString(source, "image/svg+xml");
  if (doc.getElementsByTagName("parsererror").length > 0) throw new BimiLogoError("notXml");

  const root = doc.documentElement;
  if (!root || localName(root) !== "svg") throw new BimiLogoError("notSvg");

  let minX = 0;
  let minY = 0;
  let width: number | null = null;
  let height: number | null = null;

  const viewBox = (root.getAttribute("viewBox") ?? "")
    .trim()
    .split(/[\s,]+/)
    .map(Number);
  if (viewBox.length === 4 && viewBox.every(Number.isFinite) && viewBox[2] > 0 && viewBox[3] > 0) {
    [minX, minY, width, height] = viewBox;
  } else {
    width = numberOf(root.getAttribute("width"));
    height = numberOf(root.getAttribute("height"));
  }
  if (!width || !height) throw new BimiLogoError("noSize");

  const removed = new Set<string>();
  unwrapAnchors(root, removed);
  cleanElement(root, removed);

  // Serialised whole and unwrapped, so the children carry no stray namespace
  const serialised = new XMLSerializer().serializeToString(root);
  const inner = serialised
    .replace(/^<svg\b[^>]*>/i, "")
    .replace(/<\/svg>\s*$/i, "")
    .replace(/\s+xmlns(:\w+)?="[^"]*"/g, "")
    .trim();
  if (!inner || !/<(path|rect|circle|ellipse|line|polyline|polygon|text)\b/i.test(inner)) {
    throw new BimiLogoError("empty");
  }

  // Text is drawn with whatever font the receiver has, not the one it was made in
  const warnings = /<text\b/i.test(inner) ? ["text"] : [];

  return { inner, minX, minY, width, height, removed: Array.from(removed).sort(), warnings };
}

// ------------------------------------------------------------- tracing

export type TracePreset = "high" | "balanced" | "compact" | "tiny";

/** From most faithful to smallest. A logo that does not fit the limit walks down this list. */
export const TRACE_PRESETS: TracePreset[] = ["high", "balanced", "compact", "tiny"];

const PRESET_OPTIONS: Record<TracePreset, Record<string, number>> = {
  high: { numberofcolors: 24, pathomit: 4, ltres: 1, qtres: 1, roundcoords: 1, colorsampling: 2 },
  balanced: { numberofcolors: 12, pathomit: 8, ltres: 1, qtres: 1, roundcoords: 1, colorsampling: 2 },
  compact: { numberofcolors: 6, pathomit: 16, ltres: 1.5, qtres: 1.5, roundcoords: 0, colorsampling: 2 },
  tiny: { numberofcolors: 3, pathomit: 32, ltres: 2, qtres: 2, roundcoords: 0, colorsampling: 2 },
};

/** Pixels, the same shape ImageData has, so tests need no canvas. */
export interface Raster {
  width: number;
  height: number;
  data: Uint8ClampedArray;
}

interface Tracer {
  imagedataToSVG(raster: Raster, options: Record<string, number>): string;
}

/** rgb(1, 2, 3) to #010203, which is shorter and what the validator likes best. */
function hex(rgb: string): string {
  const m = /rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)/i.exec(rgb);
  if (!m) return rgb;
  return "#" + [m[1], m[2], m[3]].map((n) => Number(n).toString(16).padStart(2, "0")).join("");
}

/**
 * Vectorises pixels. The result is the drawing only: the tracer's own wrapper
 * (and the comment it adds) is discarded, and fully transparent paths, which
 * are the empty background, are dropped.
 */
export function traceRaster(raster: Raster, preset: TracePreset, tracer: Tracer): Artwork {
  const svg = tracer.imagedataToSVG(raster, { ...PRESET_OPTIONS[preset], strokewidth: 0, viewbox: 0, scale: 1 });

  const paths: string[] = [];
  for (const match of svg.matchAll(/<path\b[^>]*\/>/g)) {
    const tag = match[0];
    const opacity = /\bopacity="([\d.]+)"/.exec(tag)?.[1];
    if (opacity !== undefined && Number(opacity) === 0) continue;
    const fill = /\bfill="([^"]+)"/.exec(tag)?.[1] ?? "#000000";
    const d = /\bd="([^"]+)"/.exec(tag)?.[1];
    if (!d) continue;
    const alpha = opacity !== undefined && Number(opacity) < 1 ? ` fill-opacity="${opacity}"` : "";
    paths.push(`<path fill="${hex(fill)}"${alpha} d="${d}"/>`);
  }
  if (paths.length === 0) throw new BimiLogoError("empty");

  return { inner: paths.join(""), minX: 0, minY: 0, width: raster.width, height: raster.height, removed: [], warnings: [] };
}

// ------------------------------------------------------------- building

export interface Layout {
  /** 1 fills 80% of the square; larger zooms in, smaller leaves more margin. */
  zoom: number;
  /** Offset from centred, as a fraction of the square. */
  dx: number;
  dy: number;
}

export const DEFAULT_LAYOUT: Layout = { zoom: 1, dx: 0, dy: 0 };

export function clampLayout(layout: Layout): Layout {
  const zoom = Math.min(4, Math.max(0.2, layout.zoom));
  // The logo may be pushed until its centre reaches an edge, no further
  return { zoom, dx: Math.min(0.5, Math.max(-0.5, layout.dx)), dy: Math.min(0.5, Math.max(-0.5, layout.dy)) };
}

const round = (n: number) => Math.round(n * 1000) / 1000;

function escapeXml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

export function isHexColor(value: string): boolean {
  return /^#[0-9a-f]{6}$/i.test(value);
}

/**
 * The final document: the artwork on a solid square, placed as laid out.
 *
 * The background is solid because a receiver draws the logo on its own
 * coloured surface and in its own mask (a circle in Gmail), and a transparent
 * mark disappears into half of them.
 */
export function buildLogoSvg(art: Artwork, layout: Layout, title: string, background: string): string {
  const { zoom, dx, dy } = clampLayout(layout);
  const fill = isHexColor(background) ? background.toLowerCase() : "#ffffff";

  const scale = ((CANVAS * FILL) / Math.max(art.width, art.height)) * zoom;
  const tx = (CANVAS - art.width * scale) / 2 + dx * CANVAS - art.minX * scale;
  const ty = (CANVAS - art.height * scale) / 2 + dy * CANVAS - art.minY * scale;

  return (
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
    `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps" viewBox="0 0 ${CANVAS} ${CANVAS}">` +
    `<title>${escapeXml(title.trim() || "Logo")}</title>` +
    `<rect width="${CANVAS}" height="${CANVAS}" fill="${fill}"/>` +
    `<g transform="matrix(${round(scale)} 0 0 ${round(scale)} ${round(tx)} ${round(ty)})">${art.inner}</g>` +
    `</svg>\n`
  );
}

/** Bytes the file will occupy, as the server counts them. */
export function byteLength(svg: string): number {
  return new TextEncoder().encode(svg).length;
}

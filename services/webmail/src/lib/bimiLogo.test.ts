import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
// eslint-disable-next-line @typescript-eslint/no-require-imports
import ImageTracer from "imagetracerjs";
import {
  BimiLogoError,
  DEFAULT_LAYOUT,
  MAX_LOGO_BYTES,
  TRACE_PRESETS,
  buildLogoSvg,
  byteLength,
  clampLayout,
  sanitizeSvg,
  traceRaster,
  type Raster,
} from "./bimiLogo";

/** A drawing from a design tool: namespaces, a style attribute, a link and a script. */
const FROM_A_DESIGN_TOOL = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape"
     xmlns:sodipodi="http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd" xmlns:xlink="http://www.w3.org/1999/xlink"
     width="200" height="100" viewBox="0 0 200 100" onload="alert(1)">
  <sodipodi:namedview id="base"/>
  <metadata>made with a tool</metadata>
  <script>alert(1)</script>
  <!-- a comment -->
  <defs><linearGradient id="g"><stop offset="0" style="stop-color:#ff0000;stop-opacity:1"/><stop offset="1" stop-color="#0000ff"/></linearGradient></defs>
  <rect x="10" y="10" width="80" height="80" style="fill:url(#g);stroke:none;filter:blur(2px)" class="big" inkscape:label="box"/>
  <a xlink:href="https://evil.test/"><circle cx="150" cy="50" r="40" fill="#123456" onclick="steal()"/></a>
  <image xlink:href="data:image/png;base64,AAAA" width="10" height="10"/>
  <use xlink:href="#g"/>
</svg>`;

describe("sanitizeSvg", () => {
  it("keeps the drawing and drops everything the format forbids", () => {
    const art = sanitizeSvg(FROM_A_DESIGN_TOOL);

    expect(art.width).toBe(200);
    expect(art.height).toBe(100);
    expect(art.inner).toContain("<rect");
    expect(art.inner).toContain("<circle");
    expect(art.inner).toContain("<linearGradient");

    for (const forbidden of ["script", "onclick", "onload", "<image", "<use", "<a ", "evil.test", "sodipodi", "inkscape", "class=", "style=", "<!--", "metadata"]) {
      expect(art.inner, forbidden).not.toContain(forbidden);
    }
    // What was lost that the person should know about
    expect(art.removed).toEqual(expect.arrayContaining(["script", "image", "use", "a (link)"]));
    // Editor noise is not worth mentioning
    expect(art.removed).not.toContain("metadata");
  });

  it("turns style declarations into attributes, and refuses ones that fetch", () => {
    const art = sanitizeSvg(
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="5" height="5" style="fill:#ff0000;opacity:.5;background:url(https://x.test/a.png)"/></svg>`,
    );
    expect(art.inner).toContain('fill="#ff0000"');
    expect(art.inner).toContain('opacity=".5"');
    expect(art.inner).not.toContain("x.test");
  });

  it("keeps a gradient reference and drops an external one", () => {
    const art = sanitizeSvg(
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="5" height="5" fill="url(#g)"/><rect width="5" height="5" fill="url(https://x.test/g)"/></svg>`,
    );
    expect(art.inner).toContain('fill="url(#g)"');
    expect(art.inner).not.toContain("x.test");
  });

  it("reads the size from width and height when there is no viewBox", () => {
    const art = sanitizeSvg(`<svg xmlns="http://www.w3.org/2000/svg" width="64px" height="32"><rect width="64" height="32"/></svg>`);
    expect([art.minX, art.minY, art.width, art.height]).toEqual([0, 0, 64, 32]);
  });

  it("keeps the origin of a viewBox that does not start at zero", () => {
    const art = sanitizeSvg(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="-10 -20 100 50"><rect x="-10" y="-20" width="100" height="50"/></svg>`);
    expect([art.minX, art.minY, art.width, art.height]).toEqual([-10, -20, 100, 50]);
  });

  it("warns that text depends on fonts", () => {
    const art = sanitizeSvg(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><text x="1" y="5">Hi</text></svg>`);
    expect(art.warnings).toEqual(["text"]);
  });

  it.each([
    ["not XML", "this is not xml <<<", "notXml"],
    ["not SVG", `<html xmlns="http://www.w3.org/1999/xhtml"><body/></html>`, "notSvg"],
    ["no size", `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`, "noSize"],
    ["nothing left", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><script>1</script></svg>`, "empty"],
  ])("refuses a file that is %s", (_name, source, code) => {
    expect(() => sanitizeSvg(source)).toThrowError(BimiLogoError);
    try {
      sanitizeSvg(source);
    } catch (error) {
      expect((error as BimiLogoError).code).toBe(code);
    }
  });
});

describe("buildLogoSvg", () => {
  const art = sanitizeSvg(FROM_A_DESIGN_TOOL);

  it("is SVG Tiny PS on a square canvas, with one title", () => {
    const svg = buildLogoSvg(art, DEFAULT_LAYOUT, "Example", "#ffffff");
    expect(svg).toContain('version="1.2"');
    expect(svg).toContain('baseProfile="tiny-ps"');
    expect(svg).toContain('viewBox="0 0 512 512"');
    expect(svg.match(/<title>/g)).toHaveLength(1);
    expect(svg).not.toMatch(/<svg[^>]*\s(x|y)=/);
  });

  it("centres a wide drawing and fills 80% of the square", () => {
    // 200x100 scaled to 409.6 wide: 2.048, centred vertically
    const svg = buildLogoSvg(art, DEFAULT_LAYOUT, "Example", "#ffffff");
    expect(svg).toContain("matrix(2.048 0 0 2.048 51.2 153.6)");
  });

  it("moves and zooms the way the person positioned it", () => {
    const base = buildLogoSvg(art, DEFAULT_LAYOUT, "Example", "#ffffff");
    const moved = buildLogoSvg(art, { zoom: 1, dx: 0.1, dy: -0.1 }, "Example", "#ffffff");
    expect(moved).toContain("matrix(2.048 0 0 2.048 102.4 102.4)");
    expect(moved).not.toBe(base);

    expect(buildLogoSvg(art, { zoom: 0.5, dx: 0, dy: 0 }, "Example", "#ffffff")).toContain("matrix(1.024 0 0 1.024");
  });

  it("accounts for a viewBox that does not start at zero", () => {
    const offset = { ...art, minX: -10, minY: -20 };
    const plain = buildLogoSvg(art, DEFAULT_LAYOUT, "x", "#fff");
    const shifted = buildLogoSvg(offset, DEFAULT_LAYOUT, "x", "#fff");
    expect(shifted).not.toBe(plain);
    // 10 units at 2.048 is 20.48 further right
    expect(shifted).toContain("matrix(2.048 0 0 2.048 71.68 194.56)");
  });

  it("escapes the title and falls back for a bad colour or an empty title", () => {
    const svg = buildLogoSvg(art, DEFAULT_LAYOUT, `A & <B> "C"`, "red; evil");
    expect(svg).toContain("<title>A &amp; &lt;B&gt; &quot;C&quot;</title>");
    expect(svg).toContain('fill="#ffffff"');
    expect(buildLogoSvg(art, DEFAULT_LAYOUT, "   ", "#000000")).toContain("<title>Logo</title>");
  });

  it("keeps the layout inside sane limits", () => {
    expect(clampLayout({ zoom: 99, dx: 9, dy: -9 })).toEqual({ zoom: 4, dx: 0.5, dy: -0.5 });
    expect(clampLayout({ zoom: 0, dx: 0, dy: 0 }).zoom).toBe(0.2);
  });

  it("counts bytes, not characters", () => {
    expect(byteLength("a")).toBe(1);
    expect(byteLength("\u00e9")).toBe(2);
  });
});

/** 64x64: a red disc on a transparent background, and a blue square. Deterministic. */
function syntheticLogo(): Raster {
  const size = 64;
  const data = new Uint8ClampedArray(size * size * 4);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const i = (y * size + x) * 4;
      const inDisc = (x - 22) ** 2 + (y - 32) ** 2 < 14 ** 2;
      const inSquare = x >= 40 && x < 58 && y >= 24 && y < 42;
      if (inDisc) data.set([220, 30, 30, 255], i);
      else if (inSquare) data.set([20, 60, 200, 255], i);
      // else: fully transparent
    }
  }
  return { width: size, height: size, data };
}

describe("traceRaster", () => {
  const tracer = ImageTracer as unknown as Parameters<typeof traceRaster>[2];

  it("vectorises pixels into paths and drops the transparent background", () => {
    const art = traceRaster(syntheticLogo(), "balanced", tracer);
    expect(art.width).toBe(64);
    expect(art.inner).toContain("<path");
    expect(art.inner).not.toContain("rgb(");
    expect(art.inner).toMatch(/fill="#[0-9a-f]{6}"/);
    // The tracer's own wrapper and comment are gone
    expect(art.inner).not.toContain("<svg");
    expect(art.inner).not.toContain("imagetracer");
    // Both colours made it, nothing is left of the empty background
    expect(art.inner.toLowerCase()).toMatch(/#dc1e1e|#d[0-9a-f]1[0-9a-f]1[0-9a-f]/);
    expect(art.inner).not.toMatch(/opacity="0"/);
  });

  it("gets smaller as the preset gets coarser", () => {
    const sizes = TRACE_PRESETS.map((preset) => byteLength(traceRaster(syntheticLogo(), preset, tracer).inner));
    for (let i = 1; i < sizes.length; i++) expect(sizes[i]).toBeLessThanOrEqual(sizes[i - 1]);
    expect(sizes[sizes.length - 1]).toBeLessThan(MAX_LOGO_BYTES);
  });

  it("refuses an image with nothing in it", () => {
    const blank: Raster = { width: 8, height: 8, data: new Uint8ClampedArray(8 * 8 * 4) };
    expect(() => traceRaster(blank, "balanced", tracer)).toThrowError(BimiLogoError);
  });
});

// The server validates what the studio produces (protocols/internal/domain/bimi).
// Both sides read the same files, so a studio that starts producing something the
// server refuses fails here or there. Regenerate with UPDATE_GOLDEN=1 after
// changing the output on purpose.
describe("what the studio produces is what the server accepts", () => {
  const dir = path.resolve(__dirname, "../../../protocols/internal/domain/bimi/testdata/studio");
  const tracer = ImageTracer as unknown as Parameters<typeof traceRaster>[2];

  const cases: Record<string, () => string> = {
    "from-a-design-tool.svg": () => buildLogoSvg(sanitizeSvg(FROM_A_DESIGN_TOOL), DEFAULT_LAYOUT, "Example", "#ffffff"),
    "positioned-and-zoomed.svg": () =>
      buildLogoSvg(sanitizeSvg(FROM_A_DESIGN_TOOL), { zoom: 1.3, dx: -0.08, dy: 0.05 }, "Example brand", "#0b5c55"),
    "traced-from-a-png.svg": () => buildLogoSvg(traceRaster(syntheticLogo(), "balanced", tracer), DEFAULT_LAYOUT, "Traced", "#ffffff"),
  };

  for (const [name, produce] of Object.entries(cases)) {
    it(name, () => {
      const output = produce();
      const file = path.join(dir, name);
      if (process.env.UPDATE_GOLDEN || !existsSync(file)) {
        mkdirSync(dir, { recursive: true });
        writeFileSync(file, output);
      }
      expect(output).toBe(readFileSync(file, "utf8"));
      expect(byteLength(output)).toBeLessThanOrEqual(MAX_LOGO_BYTES);
    });
  }
});

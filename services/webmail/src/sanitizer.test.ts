import { describe, it, expect } from "vitest";
import { sanitizeEmailHtml, blockRemoteImages, unblockRemoteImages } from "./lib/sanitizer.js";

// These run under jsdom. Under the default node environment DOMPurify was
// never reached - window was undefined - so an earlier version of this suite
// passed while only exercising a regex fallback that has since been removed.
describe("email HTML sanitizer", () => {
  it("runs in an environment that actually has a DOM", () => {
    expect(typeof window).not.toBe("undefined");
  });

  it("strips script tags and inline event handlers", () => {
    const out = sanitizeEmailHtml(
      `<div><script>alert('xss')</script><img src="x" onerror="alert(1)"><p>Safe</p></div>`,
    );
    expect(out).not.toContain("script");
    expect(out).not.toContain("onerror");
    expect(out).toContain("Safe");
  });

  // The removed regex pass built this tag out of the halves it left behind.
  it("does not reconstruct a script tag from a split one", () => {
    const out = sanitizeEmailHtml(`<scr<script>ipt>alert(1)</scr</script>ipt>`);
    expect(out.toLowerCase()).not.toContain("<script");
  });

  it("removes javascript: and data: URIs", () => {
    expect(sanitizeEmailHtml(`<a href="javascript:alert(1)">x</a>`)).not.toContain("javascript:");
    expect(sanitizeEmailHtml(`<a href="data:text/html;base64,PHN2Zz4=">x</a>`)).not.toContain("data:");
    // A legitimate link survives, or the sanitizer would be useless.
    expect(sanitizeEmailHtml(`<a href="https://example.test/x">x</a>`)).toContain("https://example.test/x");
  });

  it("removes forms, styles and base tags", () => {
    expect(sanitizeEmailHtml(`<form action="http://evil.test"><input name="p"></form>`)).not.toContain("<form");
    expect(sanitizeEmailHtml(`<style>body{display:none}</style><p>x</p>`)).not.toContain("<style");
    expect(sanitizeEmailHtml(`<base href="http://evil.test/"><p>x</p>`)).not.toContain("<base");
  });

  it("removes svg-based handlers", () => {
    const out = sanitizeEmailHtml(`<svg><animate onbegin="alert(1)" attributeName="x" dur="1s"></svg>`);
    expect(out).not.toContain("onbegin");
  });

  // Without a DOM there is nothing to parse with, so nothing may be returned:
  // "best effort" markup here would be unsanitized markup on the page.
  it("returns nothing rather than unsanitized markup when there is no DOM", () => {
    const original = globalThis.window;
    // @ts-expect-error deliberately simulating a non-browser environment
    delete globalThis.window;
    try {
      expect(sanitizeEmailHtml("<p>anything</p>")).toBe("");
    } finally {
      globalThis.window = original;
    }
  });
});

describe("remote content blocking", () => {
  it("blocks a tracking pixel until the reader opts in", () => {
    const sanitized = sanitizeEmailHtml(`<p>Test</p><img src="https://tracker.test/p.png" alt="t">`);
    const blocked = blockRemoteImages(sanitized);
    expect(blocked).toContain("data-blocked-src");
    expect(blocked).not.toMatch(/\ssrc="https:\/\/tracker\.test/);

    const unblocked = unblockRemoteImages(blocked);
    expect(unblocked).toContain('src="https://tracker.test/p.png"');
  });

  it("neutralises CSS url() references and background attributes", () => {
    // Nothing may be left that a browser would fetch: no http(s) url() and no
    // live background attribute.
    const css = blockRemoteImages(`<div style="background:url(https://t.test/p.png)">x</div>`);
    expect(css).not.toMatch(/url\(\s*['"]?https?:/i);
    const attr = blockRemoteImages(`<table background="https://t.test/p.png"><tr><td>x</td></tr></table>`);
    expect(attr).not.toMatch(/\sbackground=/i);
  });

  it("restores backgrounds when the reader opts in", () => {
    const html = `<table background="https://t.test/a.png"><tr><td style="background-image:url('https://t.test/b.png')">x</td></tr></table>`;
    expect(unblockRemoteImages(blockRemoteImages(html))).toBe(html);
  });

  it("keeps layout attributes that are not URLs", () => {
    const out = sanitizeEmailHtml(`<table width="600" cellpadding="0" bgcolor="#fff"><tr><td colspan="2" align="center"><img src="https://t.test/a.png" width="600" height="20"></td></tr></table>`);
    for (const attr of ['width="600"', 'cellpadding="0"', 'bgcolor="#fff"', 'colspan="2"', 'align="center"', 'height="20"']) {
      expect(out).toContain(attr);
    }
  });

  it("still refuses scripting schemes in attributes", () => {
    const out = sanitizeEmailHtml(`<a href="javascript:alert(1)">x</a><a href="data:text/html,<script>1</script>">y</a>`);
    expect(out).not.toMatch(/javascript:/i);
    expect(out).not.toMatch(/data:text/i);
  });
});

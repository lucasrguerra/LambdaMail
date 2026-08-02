import { describe, it, expect } from "vitest";
import { sanitizeEmailHtml, blockRemoteImages, unblockRemoteImages } from "./lib/sanitizer.js";

describe("DOMPurify HTML Sanitizer (Section 14.2)", () => {
  it("strips malicious script tags and inline event handlers", () => {
    const maliciousHtml = `<div><script>alert('xss')</script><img src="x" onerror="alert('hack')" /><p>Safe text</p></div>`;
    const sanitized = sanitizeEmailHtml(maliciousHtml);
    expect(sanitized).not.toContain("<script>");
    expect(sanitized).not.toContain("onerror");
    expect(sanitized).toContain("<p>Safe text</p>");
  });

  it("strips iframe and object tags", () => {
    const htmlWithIframe = `<div><iframe src="http://malicious.com"></iframe><object data="test.swf"></object><span>Hello</span></div>`;
    const sanitized = sanitizeEmailHtml(htmlWithIframe);
    expect(sanitized).not.toContain("<iframe");
    expect(sanitized).not.toContain("<object");
    expect(sanitized).toContain("<span>Hello</span>");
  });

  it("blocks remote tracking images by default", () => {
    const html = `<p>Test</p><img src="https://tracker.com/pixel.png" alt="tracker" />`;
    const sanitized = sanitizeEmailHtml(html);
    const blocked = blockRemoteImages(sanitized);
    expect(blocked).toContain('data-blocked-src="https://tracker.com/pixel.png"');
    expect(blocked).not.toContain(' src="https://tracker.com/pixel.png"');
  });

  it("unblocks remote images when requested by user", () => {
    const html = `<p>Test</p><img src="https://tracker.com/pixel.png" alt="tracker" />`;
    const sanitized = sanitizeEmailHtml(html);
    const blocked = blockRemoteImages(sanitized);
    const unblocked = unblockRemoteImages(blocked);
    expect(unblocked).toContain('src="https://tracker.com/pixel.png"');
  });
});

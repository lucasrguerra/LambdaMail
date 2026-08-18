import { describe, it, expect } from "vitest";
import { sanitizeEmailHtml, blockRemoteImages } from "./lib/sanitizer.js";
import { resolveInlineImages, buildReaderDocument } from "./lib/emailBody.js";

/**
 * The reader pane showed broken image icons for every message written by a
 * normal mail client. Three separate causes, one per group below: inline parts
 * were referenced as cid: and never resolved, embedded data: images were
 * stripped by the sanitizer, and the frame the body was dropped into had no
 * styles, so even a message that did render was unreadable.
 */

describe("inline (cid:) images", () => {
  it("resolves a cid reference against the parts the server returned", () => {
    const html = `<p>hi</p><img src="cid:logo@x" alt="logo">`;
    const out = resolveInlineImages(html, { "logo@x": "data:image/png;base64,AAA" });
    expect(out).toContain(`src="data:image/png;base64,AAA"`);
    expect(out).not.toContain("cid:");
  });

  // Mail clients are inconsistent about the angle brackets, and a reader that
  // only understands one spelling shows a broken icon for the other.
  it("matches a cid whether or not the reference carries angle brackets", () => {
    const out = resolveInlineImages(`<img src="cid:<sig>">`, { sig: "data:image/gif;base64,BBB" });
    expect(out).toContain("data:image/gif;base64,BBB");
  });

  // A missing part must not leave src="cid:..." on the page: the browser
  // renders that as a broken-image icon, which reads as a bug in the client.
  it("drops the source when no such part was delivered", () => {
    const out = resolveInlineImages(`<img src="cid:missing" alt="chart">`, {});
    expect(out).not.toContain("cid:missing");
    expect(out).not.toMatch(/\ssrc=/);
    expect(out).toContain('alt="chart"');
  });

  it("leaves ordinary remote and data sources untouched", () => {
    const html = `<img src="https://cdn.test/a.png"><img src="data:image/png;base64,CCC">`;
    expect(resolveInlineImages(html, {})).toBe(html);
  });
});

describe("embedded images survive sanitising", () => {
  // The URI allow-list had no data:, so DOMPurify removed the src from every
  // embedded image - including the ones this app itself inlines for cid:.
  it("keeps a data: image, which is how an inline part is delivered", () => {
    const out = sanitizeEmailHtml(`<img src="data:image/png;base64,iVBORw0KGgo=">`);
    expect(out).toContain("data:image/png;base64,iVBORw0KGgo=");
  });

  // An SVG is a document that carries script; it is the one image type that
  // must not come in through a data: URI.
  it("still refuses a data: SVG and a data: document", () => {
    expect(sanitizeEmailHtml(`<img src="data:image/svg+xml;base64,PHN2Zz4=">`)).not.toContain("data:");
    expect(sanitizeEmailHtml(`<a href="data:text/html;base64,PHN2Zz4=">x</a>`)).not.toContain("data:");
  });

  // Blocking exists to stop a remote tracking pixel. An embedded image made no
  // network request, so blocking it only breaks the message.
  it("does not block an image that is already embedded", () => {
    const blocked = blockRemoteImages(`<img src="data:image/png;base64,DDD">`);
    expect(blocked).toContain(`src="data:image/png;base64,DDD"`);
  });
});

describe("the document the reader frame is given", () => {
  const doc = buildReaderDocument("<p>Hello</p>");

  it("is a complete document, not a bare fragment", () => {
    expect(doc).toMatch(/^<!doctype html>/i);
    expect(doc).toContain("<p>Hello</p>");
    expect(doc).toContain("charset");
  });

  // Mail is written against desktop widths; without this a newsletter forces
  // the frame into a horizontal scrollbar over its own content.
  it("constrains oversized content instead of letting it overflow", () => {
    expect(doc).toContain("max-width:100%");
    expect(doc).toContain("overflow-wrap");
  });

  // A frame with sandbox="" has an opaque origin, so a link opening in place
  // strands the reader on a blank pane they cannot navigate back from.
  it("opens links in a new tab and sends no referrer", () => {
    expect(doc).toContain('target="_blank"');
    expect(doc).toContain("noreferrer");
  });

  it("renders on a light surface whatever the app theme is", () => {
    // Messages assume a white page; painting one behind them stops dark text
    // on a dark background, which is what made half the mail unreadable.
    expect(doc).toContain("#ffffff");
  });

  // The frame is sized from its content, so the page must be able to report
  // its own height back out.
  it("reports its content height to the host page", () => {
    expect(doc).toContain("postMessage");
    expect(doc).toContain("lm:reader-height");
  });
});

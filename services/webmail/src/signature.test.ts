import { describe, it, expect } from "vitest";
import { signatureToHtml, sanitizeSignature } from "./lib/signature";

/**
 * The signature is edited in one place and injected into the composer in
 * another, and the two disagreed about what it was.
 *
 * It was typed into a plain <textarea>, so it held newlines - and then it was
 * assigned with innerHTML, where a newline is whitespace and renders as
 * nothing. Every line break a reader put in their signature was silently
 * dropped the moment they composed a message.
 */

describe("a signature written as plain text", () => {
  it("keeps its line breaks when it reaches the composer", () => {
    const html = signatureToHtml("Lucas Rayan\nDesenvolvedor\nlucasrguerra.dev.br");
    expect(html).toContain("Lucas Rayan<br>");
    expect(html).toContain("Desenvolvedor<br>");
    // Rendered, the three lines must still be three lines.
    expect(html.match(/<br\s*\/?>/g) ?? []).toHaveLength(2);
  });

  it("keeps blank lines, which are deliberate spacing", () => {
    const html = signatureToHtml("Lucas\n\nDesenvolvedor");
    expect(html.match(/<br\s*\/?>/g) ?? []).toHaveLength(2);
  });

  it("escapes characters that would otherwise become markup", () => {
    const html = signatureToHtml("Lucas <lucas@example.test> & cia");
    expect(html).toContain("&lt;lucas@example.test&gt;");
    expect(html).toContain("&amp;");
  });

  it("leaves a signature that is already HTML alone", () => {
    const html = signatureToHtml("<p>Lucas <b>Rayan</b></p>");
    expect(html).toContain("<b>Rayan</b>");
    expect(html).not.toContain("&lt;p&gt;");
  });
});

describe("what a signature may contain", () => {
  it("allows the formatting a signature is actually made of", () => {
    const html = sanitizeSignature(
      '<p><b>Lucas</b> <i>Rayan</i><br><a href="https://lucasrguerra.dev.br">site</a></p>',
    );
    expect(html).toContain("<b>");
    expect(html).toContain("<i>");
    expect(html).toContain("<a");
    expect(html).toContain("https://lucasrguerra.dev.br");
  });

  it("allows images, which is half of why people want HTML here", () => {
    const html = sanitizeSignature('<img src="https://example.test/logo.png" alt="logo">');
    expect(html).toContain("<img");
    expect(html).toContain("logo.png");
  });

  it("allows an inline image pasted as a data URI", () => {
    const html = sanitizeSignature(
      '<img src="data:image/png;base64,iVBORw0KGgo=" alt="logo">',
    );
    expect(html).toContain("data:image/png;base64");
  });

  // The signature is injected into the composer as markup, so anything that
  // survives here executes on this origin the next time a message is written.
  it("strips script", () => {
    const html = sanitizeSignature('<p>Lucas</p><script>alert(1)</script>');
    expect(html).not.toContain("<script");
    expect(html).not.toContain("alert(1)");
  });

  it("strips event handlers", () => {
    const html = sanitizeSignature('<img src="x" onerror="alert(1)">');
    expect(html.toLowerCase()).not.toContain("onerror");
  });

  it("strips a javascript: link", () => {
    const html = sanitizeSignature('<a href="javascript:alert(1)">click</a>');
    expect(html.toLowerCase()).not.toContain("javascript:");
  });

  it("refuses a non-image data URI", () => {
    const html = sanitizeSignature('<a href="data:text/html,<script>alert(1)</script>">x</a>');
    expect(html.toLowerCase()).not.toContain("data:text/html");
  });

  it("returns empty for empty input rather than throwing", () => {
    expect(sanitizeSignature("")).toBe("");
  });
});

import DOMPurify from "dompurify";

/**
 * Sanitizes HTML content using DOMPurify with email-safe configuration (Section 14.2).
 * Strips scripts, dangerous attributes, and executable elements while keeping layout.
 */
export function sanitizeEmailHtml(rawHtml: string): string {
  if (typeof window === "undefined") {
    // SSR fallback: basic strip of script tags
    return rawHtml.replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, "");
  }

  return DOMPurify.sanitize(rawHtml, {
    ALLOWED_TAGS: [
      "a", "b", "blockquote", "br", "caption", "code", "div", "em", "h1", "h2", "h3",
      "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre", "span", "strong",
      "table", "tbody", "td", "tfoot", "th", "thead", "tr", "u", "ul", "style", "font",
      "sub", "sup"
    ],
    ALLOWED_ATTR: [
      "align", "alt", "bgcolor", "border", "cellpadding", "cellspacing", "cite",
      "class", "color", "colspan", "dir", "height", "href", "id", "lang", "rowspan",
      "src", "style", "title", "width", "target", "rel", "data-blocked-src"
    ],
    ALLOW_DATA_ATTR: true,
    ADD_ATTR: ["target"],
    FORBID_TAGS: ["script", "iframe", "object", "embed", "form", "input", "button"],
    FORBID_ATTR: ["onerror", "onload", "onclick", "onmouseover", "onfocus"],
  });
}

/**
 * Replaces remote image sources (http/https) with data-blocked-src to protect user privacy
 * against tracking pixels until remote images are explicitly enabled.
 */
export function blockRemoteImages(html: string): string {
  if (!html) return "";
  return html
    .replace(/(<img[^>]+)src\s*=\s*("|')https?:[^"']*\2/gi, "$1data-blocked-src=$2$2")
    .replace(/(<[^>]+)background\s*=\s*("|')https?:[^"']*\2/gi, "$1")
    .replace(/url\(\s*['"]?https?:[^)]*\)/gi, "none");
}

/**
 * Restores blocked remote image sources when user clicks "Load Remote Images".
 */
export function unblockRemoteImages(html: string): string {
  if (!html) return "";
  return html.replace(/(<img[^>]+)data-blocked-src\s*=\s*("|')([^"']+)\2/gi, '$1src="$3"');
}

import DOMPurify from "dompurify";

/**
 * Sanitizes HTML content using DOMPurify with email-safe configuration (Section 14.2).
 * Strips scripts, dangerous attributes, and executable elements while keeping layout.
 */
export function sanitizeEmailHtml(rawHtml: string): string {
  if (!rawHtml) return "";

  // Base sanitization stripping dangerous tags and inline event handlers
  let cleaned = rawHtml
    .replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, "")
    .replace(/<iframe\b[^<]*(?:(?!<\/iframe>)<[^<]*)*<\/iframe>/gi, "")
    .replace(/<object\b[^<]*(?:(?!<\/object>)<[^<]*)*<\/object>/gi, "")
    .replace(/<embed\b[^<]*(?:(?!<\/embed>)<[^<]*)*<\/embed>/gi, "")
    .replace(/\s*on\w+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi, "");

  if (typeof window !== "undefined") {
    cleaned = DOMPurify.sanitize(cleaned, {
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
      FORBID_TAGS: ["script", "iframe", "object", "embed", "form", "input", "button"],
      FORBID_ATTR: ["onerror", "onload", "onclick", "onmouseover", "onfocus"],
    });
  }

  return cleaned;
}

/**
 * Replaces remote image sources (http/https) with data-blocked-src to protect user privacy
 * against tracking pixels until remote images are explicitly enabled.
 */
export function blockRemoteImages(html: string): string {
  if (!html) return "";
  return html
    .replace(/(<img\b[^>]*?\b)src\s*=\s*("|')(https?:[^"']*)\2/gi, "$1data-blocked-src=$2$3$2")
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

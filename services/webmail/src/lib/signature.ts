import DOMPurify from "dompurify";

/**
 * The signature, on its way from the settings screen into a message.
 *
 * It used to be a plain <textarea> whose contents were assigned straight into
 * the composer with innerHTML. A newline is whitespace in HTML, so every line
 * break the reader typed was dropped the moment they wrote a message, and a
 * signature could not contain a link or an image at all.
 *
 * It is now HTML end to end, which means it has to be sanitised: whatever is
 * stored here is injected as markup into the composer on this origin every
 * time a message is written.
 */

/** Tags a signature is legitimately made of. */
const ALLOWED_TAGS = [
  "a", "b", "br", "div", "em", "i", "img", "li", "ol", "p", "span",
  "strong", "u", "small", "sub", "sup", "table", "tbody", "td", "tr", "ul", "font",
];

const ALLOWED_ATTR = [
  "alt", "align", "border", "color", "face", "height", "href", "rel", "size",
  "src", "style", "target", "title", "width",
];

/** Only images may be data URIs, and only real image types. */
const ALLOWED_DATA_IMAGE = /^data:image\/(?:png|gif|jpe?g|webp|bmp|x-icon|vnd\.microsoft\.icon);/i;

let hookInstalled = false;
function installHook(): void {
  if (hookInstalled) return;
  DOMPurify.addHook("uponSanitizeAttribute", (_node, data) => {
    if (/^\s*data:/i.test(data.attrValue) && !ALLOWED_DATA_IMAGE.test(data.attrValue.trim())) {
      // A data: URI that is not an image is a document, and a document here
      // is script on this origin.
      data.keepAttr = false;
    }
  });
  hookInstalled = true;
}

/**
 * Cleans a signature for storage and for injection.
 *
 * Fails closed without a DOM: returning the input unsanitised would put raw
 * markup into the composer, which is the one outcome worth avoiding.
 */
export function sanitizeSignature(html: string): string {
  if (!html) return "";
  if (typeof window === "undefined" || !DOMPurify.isSupported) return "";
  installHook();
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS,
    ALLOWED_ATTR,
    // Keeps the sanitiser from being talked into treating markup as SVG/MathML,
    // where the tag allowlist above does not apply the same way.
    USE_PROFILES: { html: true },
  });
}

/**
 * Whether a stored signature is markup or the plain text of the old field.
 *
 * Matched against real tag names rather than "a < followed by a letter": a
 * signature that names an address the ordinary way - Lucas <lucas@example.test>
 * - satisfies the loose test, and treating it as markup made the sanitiser
 * read <lucas@example.test> as an unknown tag and delete the address.
 */
const HTML_TAG = new RegExp(
  `</?(?:${ALLOWED_TAGS.join("|")}|h[1-6]|hr|blockquote|pre|code)\\b[^>]*>`,
  "i",
);

function looksLikeHtml(value: string): boolean {
  return HTML_TAG.test(value);
}

function escapeText(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/**
 * The signature as markup, whichever form it was stored in.
 *
 * Signatures written before this was an HTML field are plain text with real
 * newlines, and they have to keep working: each newline becomes a <br>, and
 * the text itself is escaped so an address written as <me@example.test> does
 * not disappear into a tag that never closes.
 */
export function signatureToHtml(stored: string): string {
  const value = stored ?? "";
  if (!value.trim()) return "";
  if (looksLikeHtml(value)) return sanitizeSignature(value);
  // Escaped text is safe by construction, and it must not be sanitised again:
  // the sanitiser decodes the entities first, so an address written as
  // <me@example.test> would be read back as an unknown tag and dropped - which
  // is how a signature could lose the very line it was there to carry.
  return escapeText(value).replace(/\r?\n/g, "<br>");
}

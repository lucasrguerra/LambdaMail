import DOMPurify from "dompurify";

/**
 * Sanitizes message HTML with DOMPurify (PLAN.md section 14.2).
 *
 * There is deliberately no hand-rolled regex pass in front of DOMPurify. The
 * previous one turned "<scr<script>ipt>" into a working "<script>" by deleting
 * the inner match and joining the halves - it manufactured the tag it was
 * meant to remove. It also left javascript: URIs and <form> untouched, because
 * a regex cannot parse HTML. DOMPurify parses, which is why it is the only
 * thing here.
 */
/**
 * The data: URIs a message body may carry: raster images only.
 *
 * DOMPurify allows data: on an image source from its own built-in list, which
 * includes image/svg+xml, and that list is consulted instead of
 * ALLOWED_URI_REGEXP - so tightening the regexp alone does not exclude an SVG.
 * The hook below is what actually refuses it, and an SVG has to be refused: it
 * is a document that carries script, not a picture.
 */
const ALLOWED_DATA_IMAGE = /^data:image\/(?:png|gif|jpe?g|webp|bmp|x-icon|vnd\.microsoft\.icon);/i;

let hookInstalled = false;

function installDataUriHook(): void {
  if (hookInstalled) return;
  DOMPurify.addHook("uponSanitizeAttribute", (_node, data) => {
    if (/^\s*data:/i.test(data.attrValue) && !ALLOWED_DATA_IMAGE.test(data.attrValue.trim())) {
      data.keepAttr = false;
    }
  });
  hookInstalled = true;
}

export function sanitizeEmailHtml(rawHtml: string): string {
  if (!rawHtml) return "";

  // Without a DOM there is nothing to parse with, so nothing is returned.
  // Failing closed matters: returning "best effort" markup would put
  // unsanitized HTML on the page whenever this ran outside the browser.
  if (typeof window === "undefined" || !DOMPurify.isSupported) {
    return "";
  }

  installDataUriHook();

  return DOMPurify.sanitize(rawHtml, {
    ALLOWED_TAGS: [
      "a", "b", "blockquote", "br", "caption", "code", "div", "em", "h1", "h2", "h3",
      "h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre", "span", "strong",
      "table", "tbody", "td", "tfoot", "th", "thead", "tr", "u", "font", "sub", "sup",
    ],
    ALLOWED_ATTR: [
      "align", "alt", "bgcolor", "border", "cellpadding", "cellspacing", "cite",
      "class", "color", "colspan", "dir", "height", "href", "lang", "rowspan",
      "src", "style", "title", "width", "target", "rel", "data-blocked-src",
    ],
    // style, link, meta and base are gone: a stylesheet in a message can cover
    // the rest of the frame, and <base> rewrites every relative URL in it.
    // id goes too, so a message cannot collide with the host document's ids.
    FORBID_TAGS: ["script", "iframe", "object", "embed", "form", "input", "button", "style", "link", "meta", "base"],
    FORBID_ATTR: ["srcset", "formaction", "ping", "id"],
    ALLOW_DATA_ATTR: false,
    // data: is how an anchor smuggles a document onto this origin, so only the
    // raster image types are let through - which is how this app delivers a
    // message's own inline parts, and rejecting them stripped the src from
    // every embedded picture. image/svg+xml is deliberately absent: an SVG is
    // a document that carries script.
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto|cid|tel):|data:image\/(?:png|gif|jpe?g|webp|bmp|x-icon|vnd\.microsoft\.icon);)/i,
  });
}

/**
 * Replaces remote image sources with data-blocked-src so a tracking pixel
 * cannot fire before the reader asks for images.
 */
export function blockRemoteImages(html: string): string {
  if (!html) return "";
  return html
    .replace(/(<img\b[^>]*?\b)src\s*=\s*("|')(https?:[^"']*)\2/gi, "$1data-blocked-src=$2$3$2")
    .replace(/(<[^>]+)background\s*=\s*("|')https?:[^"']*\2/gi, "$1")
    .replace(/url\(\s*['"]?https?:[^)]*\)/gi, "none");
}

/** Restores the blocked sources once the reader opts in. */
export function unblockRemoteImages(html: string): string {
  if (!html) return "";
  return html.replace(/(<img[^>]+)data-blocked-src\s*=\s*("|')([^"']+)\2/gi, "$1src=$2$3$2");
}

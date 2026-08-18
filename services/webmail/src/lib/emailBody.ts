/**
 * Turns a message's sanitized HTML into something a frame can actually show.
 *
 * Two things were missing and both looked like "images are broken": a message
 * written by any normal mail client references its own pictures as
 * src="cid:...", which resolves against nothing in a browser; and the frame was
 * handed a bare fragment with no document around it, so mail written for a
 * white page rendered as dark text on a dark surface with no width limit.
 */

/** The parts the API returns alongside the body, keyed by Content-ID. */
export type InlineImages = Record<string, string>;

/**
 * Replaces src="cid:x" with the data: URI the server delivered for that part.
 *
 * A reference with no matching part loses its source entirely rather than
 * keeping a cid: the browser cannot fetch: an unresolvable src draws a
 * broken-image icon, while no src at all falls back to the alt text.
 */
export function resolveInlineImages(html: string, images: InlineImages): string {
  if (!html) return "";

  return html.replace(
    /(<img\b[^>]*?)\ssrc\s*=\s*("|')cid:([^"']*)\2/gi,
    (_match, head: string, _quote: string, reference: string) => {
      // Clients disagree about whether the reference keeps the angle brackets
      // the Content-ID header carries, so both spellings resolve.
      const key = decodeURIComponent(reference.trim()).replace(/^<|>$/g, "");
      const resolved = images[key];
      return resolved ? `${head} src="${resolved}"` : head;
    },
  );
}

/**
 * Wraps a message body in the document the reader frame renders.
 *
 * The frame is sandboxed with an opaque origin, so it cannot inherit anything
 * from the app: every style, the light surface mail assumes, and the height
 * report the host page sizes the frame from all have to travel inside it.
 */
export function buildReaderDocument(bodyHtml: string): string {
  return `<!doctype html>
<html><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<!-- Every link leaves this document: it has an opaque origin, so navigating in
     place strands the reader on a blank pane with no way back. Declared here
     rather than only in script so it holds even if script does not run. -->
<base target="_blank">
<!-- Any remote resource the reader opted into must not carry the message's URL
     with it, and this holds without script. -->
<meta name="referrer" content="no-referrer">
<style>
  html { background:#ffffff; }
  body {
    margin:0; padding:20px; background:#ffffff; color:#1e293b;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    font-size:14px; line-height:1.6;
    /* Mail is written against desktop widths. Without these a newsletter
       pushes a horizontal scrollbar across its own content. */
    overflow-wrap:anywhere; word-break:break-word;
  }
  img, video, table, pre { max-width:100% !important; }
  img { height:auto; }
  table { border-collapse:collapse; }
  /* A layout table with a fixed pixel width is the usual overflow culprit. */
  table[width] { width:auto !important; max-width:100% !important; }
  blockquote {
    margin:0 0 0 4px; padding-left:12px;
    border-left:3px solid #cbd5e1; color:#475569;
  }
  a { color:#4f46e5; }
  pre { white-space:pre-wrap; }
</style>
</head>
<body>
${bodyHtml}
<script>
  // <base> already sends the click to a new tab; this adds the rel that keeps
  // the destination from learning where the click came from.
  for (const link of document.querySelectorAll("a[href]")) {
    link.setAttribute("rel", "noopener noreferrer");
  }

  // The host sizes the frame from this rather than guessing: a fixed height
  // either clips a long message or leaves a slab of empty white under a short
  // one, and a sandboxed frame cannot be measured from outside.
  const report = () => parent.postMessage(
    { type: "lm:reader-height", height: document.documentElement.scrollHeight }, "*");
  report();
  window.addEventListener("load", report);
  if (typeof ResizeObserver === "function") new ResizeObserver(report).observe(document.body);
</script>
</body></html>`;
}

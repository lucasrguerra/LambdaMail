/**
 * Reading and answering address lists.
 *
 * The compose field used to split on every comma, which cuts a display name
 * like `"Silva, Joao" <joao@x.test>` in half and sends to two broken
 * addresses. Reply-all had no home at all: the composer took one `replyTo`
 * and nothing else, so answering a thread meant retyping everyone on it.
 */

export interface ParsedAddress {
  /** The entry as it will be sent: `Name <addr>` or a bare address. */
  entry: string;
  /** The mailbox itself, lowercased for comparison. */
  address: string;
  /** What a chip shows: the name when there is one, else the address. */
  label: string;
  valid: boolean;
}

const MAILBOX = /^[^\s@<>(),;:"\\[\]]+@[^\s@<>(),;:"\\[\]]+\.[^\s@<>(),;:"\\[\]]+$/;

export function isValidAddress(address: string): boolean {
  return address.length <= 254 && MAILBOX.test(address.trim());
}

/** The mailbox inside `Name <addr>`, or the text itself when it is bare. */
export function addressOf(entry: string): string {
  const angled = entry.match(/<([^>]*)>\s*$/);
  return (angled ? angled[1] : entry).trim().toLowerCase();
}

function nameOf(entry: string): string {
  const angled = entry.match(/^\s*"?(.*?)"?\s*<[^>]*>\s*$/);
  return angled ? angled[1].replace(/\\(.)/g, "$1").trim() : "";
}

/**
 * Splits text on commas, semicolons and line breaks that are not inside a
 * quoted name or an angle-bracketed address.
 */
function splitOutsideQuotes(text: string): string[] {
  const parts: string[] = [];
  let current = "";
  let quoted = false;
  let angled = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted && ch === "\\" && i + 1 < text.length) {
      current += ch + text[++i];
      continue;
    }
    if (ch === '"') quoted = !quoted;
    else if (!quoted && ch === "<") angled = true;
    else if (!quoted && ch === ">") angled = false;

    if (!quoted && !angled && (ch === "," || ch === ";" || ch === "\n" || ch === "\r")) {
      parts.push(current);
      current = "";
    } else {
      current += ch;
    }
  }
  parts.push(current);
  return parts.map((p) => p.trim()).filter(Boolean);
}

export function parseAddress(raw: string): ParsedAddress {
  const entry = raw.trim();
  const address = addressOf(entry);
  const name = nameOf(entry);
  return { entry, address, label: name || address, valid: isValidAddress(address) };
}

/**
 * Parses pasted or typed text into addresses.
 *
 * A run of bare addresses separated only by spaces - what copying from a mail
 * header or a spreadsheet produces - is split as well, but only when every
 * piece is itself an address, so a display name with spaces is never broken.
 */
export function parseAddressList(text: string): ParsedAddress[] {
  const out: ParsedAddress[] = [];
  for (const part of splitOutsideQuotes(text)) {
    const pieces = part.split(/\s+/);
    if (pieces.length > 1 && !/[<>"]/.test(part) && pieces.every((p) => isValidAddress(p))) {
      for (const p of pieces) out.push(parseAddress(p));
    } else {
      out.push(parseAddress(part));
    }
  }
  return out;
}

/** The list without repeats, comparing mailboxes and keeping the first entry. */
export function dedupe(entries: string[]): string[] {
  const seen = new Set<string>();
  return entries.filter((entry) => {
    const key = addressOf(entry);
    if (!key || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

interface Thread {
  from: string;
  to: string[];
  cc: string[];
}

export interface Recipients {
  to: string[];
  cc: string[];
}

/**
 * Who a plain reply goes to: the sender. Answering a message the user wrote
 * themselves - from Sent - means following up with its recipients, not
 * writing to oneself.
 */
export function replyRecipients(message: Thread, own: string): Recipients {
  const me = own.trim().toLowerCase();
  if (me && addressOf(message.from) === me) {
    return { to: dedupe(message.to), cc: [] };
  }
  return { to: [message.from], cc: [] };
}

/**
 * Who reply-all goes to: the sender and everyone else who was addressed.
 *
 * The user's own address is dropped everywhere - mailing oneself on every
 * reply is the classic reply-all mistake - and nobody is listed twice, in
 * To and again in Cc.
 */
export function replyAllRecipients(message: Thread, own: string): Recipients {
  const me = own.trim().toLowerCase();
  const notMe = (entry: string) => addressOf(entry) !== me;

  const base = replyRecipients(message, own);
  const to = dedupe([...base.to, ...message.to].filter(notMe));
  const taken = new Set(to.map(addressOf));
  const cc = dedupe(message.cc.filter(notMe)).filter((entry) => !taken.has(addressOf(entry)));
  return { to, cc };
}

/** Whether reply-all would reach anyone a plain reply would not. */
export function offersReplyAll(message: Thread, own: string): boolean {
  const single = replyRecipients(message, own);
  const all = replyAllRecipients(message, own);
  const reached = new Set(single.to.map(addressOf));
  return [...all.to, ...all.cc].some((entry) => !reached.has(addressOf(entry)));
}

/** `Re:` or `Fwd:` once, however many times the subject has been through. */
export function prefixSubject(subject: string, mode: "reply" | "forward"): string {
  if (!subject) return "";
  if (mode === "forward") return `Fwd: ${subject.replace(/^((Fwd?|Enc|Rv):\s*)+/i, "")}`;
  return `Re: ${subject.replace(/^((Re|Res):\s*)+/i, "")}`;
}

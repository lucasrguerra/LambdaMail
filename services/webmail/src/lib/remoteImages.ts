/**
 * Which senders may load their remote images.
 *
 * Remote images are a read receipt the sender did not ask permission for: a
 * unique URL per recipient tells them the message was opened, when, and from
 * roughly where. So the default is blocked and stays blocked - only an
 * explicit decision by the reader changes it.
 *
 * What was missing was memory. The choice was reset every time a message was
 * opened, so the same newsletter had to be unblocked again on every read, and
 * there was no way to say "this sender is fine, stop asking".
 *
 * The decision is per sender rather than per message, which is how mail
 * clients have always framed it: the question a reader is really answering is
 * whether they trust who sent it.
 */

export const TRUSTED_SENDERS_KEY = "lm_remote_images_trusted";

/** How many senders are remembered before the oldest decisions are dropped. */
const MAX_REMEMBERED = 200;

/**
 * The address inside a From header, lowercased.
 *
 * Headers arrive as `Jenny <jenny@example.test>` or as a bare address, and the
 * same sender must resolve to one key either way or a decision made on one
 * message would not apply to the next.
 */
export function senderKey(from: string): string {
  const raw = (from ?? "").trim();
  if (!raw) return "";
  const angled = raw.match(/<([^>]+)>/);
  const address = (angled ? angled[1] : raw).trim().toLowerCase();
  return address.includes("@") ? address : "";
}

/**
 * Reads the stored list.
 *
 * Every access is guarded: a private window, cleared site data, or a browser
 * set to refuse storage all throw here, and none of them is a reason for the
 * reader to fail to render. Failing closed means falling back to blocked,
 * which is the safe direction.
 */
function readTrusted(): string[] {
  try {
    const raw = window.localStorage.getItem(TRUSTED_SENDERS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((v) => typeof v === "string") : [];
  } catch {
    return [];
  }
}

function writeTrusted(list: string[]): void {
  try {
    // Newest first, capped: a mailbox read for years would otherwise grow this
    // list without limit, and the oldest decisions are the least likely to
    // still reflect what the reader wants.
    window.localStorage.setItem(
      TRUSTED_SENDERS_KEY,
      JSON.stringify(list.slice(0, MAX_REMEMBERED)),
    );
  } catch {
    // Nothing is remembered this session; the reader still works.
  }
}

export function isSenderTrusted(from: string): boolean {
  const key = senderKey(from);
  if (!key) return false;
  return readTrusted().includes(key);
}

export function trustSender(from: string): void {
  const key = senderKey(from);
  if (!key) return;
  const current = readTrusted().filter((entry) => entry !== key);
  writeTrusted([key, ...current]);
}

export function revokeSender(from: string): void {
  const key = senderKey(from);
  if (!key) return;
  writeTrusted(readTrusted().filter((entry) => entry !== key));
}

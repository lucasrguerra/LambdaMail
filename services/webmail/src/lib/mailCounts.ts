/**
 * The arithmetic behind every number the mail screens display.
 *
 * It lives here, apart from the components, because each of these rules was
 * got wrong inside a JSX expression where nothing could test it: the sidebar
 * resolved a folder by a role that folders do not always carry, the list header
 * printed the length of the page it had loaded as though it were the size of
 * the folder, and marking a message read moved no counter anywhere.
 */

/** A folder as /api/v1/mail/folders returns it. */
export interface FolderSummary {
  special_use: string;
  name: string;
  unread_count: number;
  total_count: number;
}

/** The fields of a message row these counts depend on. */
export interface MessageSummary {
  uid: number;
  seen: boolean;
  has_attachments: boolean;
}

export interface FolderMetrics {
  unread: number;
  total: number;
}

export type ListFilter = "unread" | "attachment" | null;

/** Clamps a counter to something displayable: no negatives, no NaN. */
function counter(value: unknown): number {
  const numeric = typeof value === "number" && Number.isFinite(value) ? value : 0;
  return Math.max(0, Math.trunc(numeric));
}

/**
 * The counts for one folder, addressed the way the URL addresses it.
 *
 * A folder is matched on its special-use role or, failing that, its name: a
 * folder an IMAP client created has no role, and matching only on the role left
 * every one of them without a badge. The name comparison is case-insensitive
 * because IMAP spells the inbox INBOX and the route spells it inbox.
 */
export function folderMetrics(
  folders: FolderSummary[] | undefined | null,
  route: string,
): FolderMetrics {
  const wanted = route.trim().toLowerCase();
  const match = (folders ?? []).find(
    (f) => f.special_use?.toLowerCase() === wanted || f.name?.toLowerCase() === wanted,
  );
  if (!match) return { unread: 0, total: 0 };
  return { unread: counter(match.unread_count), total: counter(match.total_count) };
}

/**
 * The number a sidebar folder shows, or 0 for no badge.
 *
 * Drafts is the exception: nothing ever marks a draft read, so its unread count
 * is permanently zero and the badge never appeared - while the number that
 * matters there, how many unfinished messages are waiting, was already on hand.
 */
export function badgeCount(folders: FolderSummary[] | undefined | null, route: string): number {
  const { unread, total } = folderMetrics(folders, route);
  return route.trim().toLowerCase() === "drafts" ? total : unread;
}

/** What is on the page that has been loaded, as opposed to what is in the folder. */
export function messageCounts(messages: MessageSummary[]): {
  loaded: number;
  unread: number;
  withAttachments: number;
} {
  return {
    loaded: messages.length,
    unread: messages.filter((m) => !m.seen).length,
    withAttachments: messages.filter((m) => m.has_attachments).length,
  };
}

export function filterMessages<T extends MessageSummary>(messages: T[], filter: ListFilter): T[] {
  if (filter === "unread") return messages.filter((m) => !m.seen);
  if (filter === "attachment") return messages.filter((m) => m.has_attachments);
  return messages;
}

/**
 * The count beside the list heading, and whether it describes the folder.
 *
 * Two numbers were being conflated. The size of the folder comes from the
 * folder list; the number of rows on screen is whatever the API returned for
 * this page, capped at 50 - so a large folder advertised 50 messages. During a
 * search neither the folder total nor a cap is meaningful: what the reader
 * wants is how many matched.
 *
 * The folder counter is denormalised and can lag, so it is never allowed to
 * claim fewer messages than are already visible.
 */
export function listHeaderCount(input: {
  folder: FolderMetrics;
  loaded: number;
  searching: boolean;
}): { count: number; isFolderTotal: boolean } {
  if (input.searching) return { count: counter(input.loaded), isFolderTotal: false };
  return {
    count: Math.max(counter(input.folder.total), counter(input.loaded)),
    isFolderTotal: true,
  };
}

/**
 * The list with one message's read flag changed.
 *
 * A new array, because React reads this state: mutating in place left the row
 * bold until some unrelated change forced a render.
 */
export function applySeen<T extends MessageSummary>(messages: T[], uid: number, seen: boolean): T[] {
  return messages.map((m) => (m.uid === uid ? { ...m, seen } : m));
}

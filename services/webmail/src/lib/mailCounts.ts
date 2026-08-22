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

/** What the sidebar shows beside one folder. */
export interface FolderBadge {
  /** How many unread messages the folder holds. */
  unread: number;
  /** How many messages it holds in total. */
  total: number;
  /**
   * Whether the unread number is worth showing at all.
   *
   * Kept apart from the number itself because the sidebar used to fall back to
   * printing the total in the unread badge's place whenever nothing was
   * unread. An inbox holding fourteen read messages therefore showed "14", and
   * reading another message never moved it - the number was never counting
   * what the user thought it was.
   */
  showsUnread: boolean;
}

/**
 * The two numbers a sidebar folder has, and which of them to emphasise.
 *
 * Drafts deliberately has no unread badge: nothing marks a draft read, so its
 * unread count is always zero and the number that matters there is how many
 * unfinished messages are waiting - which is the total. Returning showsUnread
 * false for it is what stops the sidebar rendering that same total twice, once
 * as the size and once as the badge, which is where "Rascunhos 1 1" came from.
 */
export function folderBadge(
  folders: FolderSummary[] | undefined | null,
  route: string,
): FolderBadge {
  const { unread, total } = folderMetrics(folders, route);
  const isDrafts = route.trim().toLowerCase() === "drafts";
  return { unread, total, showsUnread: !isDrafts && unread > 0 };
}

/** Which actions a message offers, decided by the folder it is in. */
export interface ReaderActions {
  canReply: boolean;
  canForward: boolean;
  /** Reopen an unfinished message in the composer. */
  canEdit: boolean;
  canMarkUnread: boolean;
  canDelete: boolean;
  /** True in Trash, where delete destroys rather than moves. */
  deleteIsPermanent: boolean;
}

/**
 * The actions that make sense for a message, given where it lives.
 *
 * The reader offered the same four buttons everywhere, so a message the user
 * had sent themselves could be "marked unread" - a flag nothing reads on a
 * folder nothing delivers to - and a half-written draft offered Reply and
 * Forward but no way to carry on writing it. Meanwhile delete was offered
 * nowhere at all.
 */
export function readerActions(folder: string): ReaderActions {
  const role = folder.trim().toLowerCase();
  const isDrafts = role === "drafts";
  const isOwnCopy = isDrafts || role === "sent";

  return {
    canReply: !isDrafts,
    canForward: !isDrafts,
    canEdit: isDrafts,
    // Sent mail and drafts are the user's own copies. Nothing ever delivers
    // to those folders, so an unread flag there conveys nothing.
    canMarkUnread: !isOwnCopy,
    canDelete: true,
    deleteIsPermanent: role === "trash",
  };
}

/**
 * The folders a message may be filed into, from where it is now.
 *
 * Sent and Drafts are never offered. They record how a message came to exist
 * rather than where its reader put it: moving arbitrary mail into Sent would
 * have the folder claim it was sent from here, and a message in Drafts that
 * cannot be edited would sit among the unfinished ones forever. Moving *out*
 * of them is fine, which is why the exclusion is on the destination only.
 *
 * Matching is by name as well as role, so a folder the user created - which
 * has no special-use role at all - is a destination like any other.
 */
export function moveTargets(
  folders: FolderSummary[] | undefined | null,
  currentRoute: string,
): FolderSummary[] {
  const current = currentRoute.trim().toLowerCase();
  return (folders ?? []).filter((folder) => {
    const role = (folder.special_use ?? "").toLowerCase();
    const name = (folder.name ?? "").toLowerCase();
    if (role === "sent" || role === "drafts") return false;
    if (name === "sent" || name === "drafts") return false;
    // Already here: nothing to do.
    //
    // Guarded on current being non-empty. A folder the user created has no
    // special-use role, so with an empty current `role === current` was true
    // for every one of them - and the rules screen, which has no open message
    // and therefore no current folder, offered a destination list missing
    // precisely the folders a filing rule is usually written for.
    if (current !== "" && (role === current || name === current)) return false;
    return Boolean(folder.name);
  });
}

/**
 * The folders the user created for themselves.
 *
 * The sidebar lists the standard folders from a fixed array, so anything the
 * user made appears nowhere unless it is picked out separately - which is why
 * a rule could name a folder that had no way of being seen or created.
 *
 * A custom folder is one with no special-use role, minus the folders this
 * server creates for its own purposes: Reports is filled by the report
 * ingestion and is not the user's to rename or delete.
 */
const SYSTEM_OWNED = new Set(["reports", "inbox", "sent", "drafts", "trash", "junk", "archive"]);

export function customFolders(folders: FolderSummary[] | undefined | null): FolderSummary[] {
  return (folders ?? [])
    .filter((folder) => {
      const role = (folder.special_use ?? "").trim();
      const name = (folder.name ?? "").trim().toLowerCase();
      return role === "" && name !== "" && !SYSTEM_OWNED.has(name);
    })
    .sort((a, b) => a.name.localeCompare(b.name));
}

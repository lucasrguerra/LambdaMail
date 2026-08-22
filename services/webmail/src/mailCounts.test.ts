import { describe, it, expect } from "vitest";
import {
  folderMetrics,
  badgeCount,
  messageCounts,
  filterMessages,
  listHeaderCount,
  applySeen,
  type FolderSummary,
  type MessageSummary,
} from "./lib/mailCounts.js";

/**
 * The counters the interface shows.
 *
 * Every case here is something the screens got wrong: the sidebar matched a
 * folder by a role it does not always carry, the message list printed the
 * length of the page it had loaded as if it were the size of the folder, that
 * number then changed while typing in the search box, and marking a message
 * read moved no counter at all.
 */

const folders: FolderSummary[] = [
  { special_use: "inbox", name: "INBOX", unread_count: 3, total_count: 42 },
  { special_use: "drafts", name: "Drafts", unread_count: 0, total_count: 2 },
  { special_use: "", name: "Projects", unread_count: 5, total_count: 9 },
];

describe("resolving a folder", () => {
  it("finds it by its special-use role", () => {
    expect(folderMetrics(folders, "inbox")).toEqual({ unread: 3, total: 42 });
  });

  // A folder created by an IMAP client has no special-use role at all, so the
  // route segment is all there is to match on.
  it("falls back to the folder name when there is no role", () => {
    expect(folderMetrics(folders, "projects")).toEqual({ unread: 5, total: 9 });
  });

  it("matches the name regardless of case, as IMAP INBOX requires", () => {
    expect(folderMetrics(folders, "Inbox").total).toBe(42);
    expect(folderMetrics([{ special_use: "", name: "INBOX", unread_count: 1, total_count: 7 }], "inbox").total)
      .toBe(7);
  });

  // Before the folder list has loaded there is nothing to read counts from.
  // Zero is the honest answer; NaN rendered as "NaN" in the badge.
  it("reports zeros rather than NaN for a folder it does not have yet", () => {
    expect(folderMetrics([], "inbox")).toEqual({ unread: 0, total: 0 });
    expect(folderMetrics(undefined, "inbox")).toEqual({ unread: 0, total: 0 });
  });

  // A counter column can drift below zero on a bad migration; the interface
  // must not be the place that publishes it.
  it("never reports a negative count", () => {
    const broken: FolderSummary[] = [{ special_use: "inbox", name: "INBOX", unread_count: -4, total_count: -1 }];
    expect(folderMetrics(broken, "inbox")).toEqual({ unread: 0, total: 0 });
  });
});

describe("the number on a sidebar folder", () => {
  it("is the unread count for a folder that receives mail", () => {
    expect(badgeCount(folders, "inbox")).toBe(3);
  });

  // Nothing marks a draft read, so its unread count is always zero and the
  // badge was always absent - while the useful number, how many unfinished
  // messages are waiting, was on hand the whole time.
  it("is the total for Drafts, which has no notion of unread", () => {
    expect(badgeCount(folders, "drafts")).toBe(2);
  });

  it("is absent, not a zero, when there is nothing to report", () => {
    expect(badgeCount(folders, "sent")).toBe(0);
    expect(badgeCount([{ special_use: "drafts", name: "Drafts", unread_count: 0, total_count: 0 }], "drafts"))
      .toBe(0);
  });
});

const messages: MessageSummary[] = [
  { uid: 1, seen: false, has_attachments: true },
  { uid: 2, seen: true, has_attachments: false },
  { uid: 3, seen: false, has_attachments: false },
];

describe("counting the loaded page", () => {
  it("counts what is on it by unread and by attachment", () => {
    expect(messageCounts(messages)).toEqual({ loaded: 3, unread: 2, withAttachments: 1 });
  });

  it("counts an empty page as zeros", () => {
    expect(messageCounts([])).toEqual({ loaded: 0, unread: 0, withAttachments: 0 });
  });
});

describe("filtering the list", () => {
  it("keeps everything when no filter is set", () => {
    expect(filterMessages(messages, null)).toHaveLength(3);
  });

  it("keeps only unread, and only messages with attachments", () => {
    expect(filterMessages(messages, "unread").map((m) => m.uid)).toEqual([1, 3]);
    expect(filterMessages(messages, "attachment").map((m) => m.uid)).toEqual([1]);
  });
});

describe("the count in the list header", () => {
  // The header printed messages.length, which is the size of the page the API
  // returned - capped at 50 - so a folder holding 4000 messages advertised 50.
  it("is the folder's size, not the size of the page that was loaded", () => {
    expect(listHeaderCount({ folder: { unread: 12, total: 4000 }, loaded: 50, searching: false })).toEqual({
      count: 4000,
      isFolderTotal: true,
    });
  });

  // While searching, the folder total is not what the reader is looking at, and
  // showing it made the number jump around as they typed.
  it("is the number of matches while a search is active", () => {
    expect(listHeaderCount({ folder: { unread: 3, total: 42 }, loaded: 2, searching: true })).toEqual({
      count: 2,
      isFolderTotal: false,
    });
  });

  // A folder whose counter column is behind - it is denormalised - must not
  // claim fewer messages than the page already on screen.
  it("never reports fewer messages than are visible", () => {
    expect(listHeaderCount({ folder: { unread: 0, total: 1 }, loaded: 12, searching: false })).toEqual({
      count: 12,
      isFolderTotal: true,
    });
  });
});

describe("marking a message read in the loaded list", () => {
  it("flips the flag on that message only", () => {
    const next = applySeen(messages, 1, true);
    expect(next.find((m) => m.uid === 1)?.seen).toBe(true);
    expect(next.find((m) => m.uid === 3)?.seen).toBe(false);
  });

  it("reduces the unread count of the page it returns", () => {
    expect(messageCounts(applySeen(messages, 1, true)).unread).toBe(1);
  });

  it("can put a message back to unread", () => {
    expect(messageCounts(applySeen(messages, 2, false)).unread).toBe(3);
  });

  // The list state is read by React, so a mutation in place would not re-render
  // and the row would keep its bold styling until something else moved.
  it("returns a new array rather than mutating the one it was given", () => {
    const next = applySeen(messages, 1, true);
    expect(next).not.toBe(messages);
    expect(messages.find((m) => m.uid === 1)?.seen).toBe(false);
  });
});

// --- what the sidebar actually renders -----------------------------------

import { folderBadge, readerActions, moveTargets, customFolders } from "./lib/mailCounts";

/**
 * These reproduce what the running webmail shows, which the badgeCount tests
 * above could not: they check the number, and the defect was in how the number
 * is displayed.
 */
describe("the badge a sidebar folder renders", () => {
  const folders = [
    { special_use: "inbox", name: "INBOX", unread_count: 0, total_count: 14 },
    { special_use: "drafts", name: "Drafts", unread_count: 0, total_count: 1 },
    { special_use: "sent", name: "Sent", unread_count: 0, total_count: 1 },
    { special_use: "junk", name: "Junk", unread_count: 3, total_count: 9 },
    { special_use: "archive", name: "Archive", unread_count: 0, total_count: 0 },
  ];

  // The inbox showed "14" with nothing unread. Reading every message left it
  // at 14, because 14 was the total wearing the unread badge's clothes.
  it("does not show a total where an unread count belongs", () => {
    const badge = folderBadge(folders, "inbox");
    expect(badge.unread).toBe(0);
    expect(badge.showsUnread).toBe(false);
  });

  it("still reports the folder size, separately from the unread count", () => {
    expect(folderBadge(folders, "inbox").total).toBe(14);
  });

  // Drafts counted total as its badge, so total and badge were the same
  // number and the sidebar printed it twice: "Rascunhos 1 1".
  it("never renders the same number twice for drafts", () => {
    const badge = folderBadge(folders, "drafts");
    const rendered = [badge.showsUnread ? badge.unread : null, badge.total].filter(
      (v) => v !== null,
    );
    expect(rendered).toEqual([1]);
  });

  it("shows the unread count when there really is unread mail", () => {
    const badge = folderBadge(folders, "junk");
    expect(badge.showsUnread).toBe(true);
    expect(badge.unread).toBe(3);
    expect(badge.total).toBe(9);
  });

  it("shows nothing at all for an empty folder", () => {
    const badge = folderBadge(folders, "archive");
    expect(badge.showsUnread).toBe(false);
    expect(badge.total).toBe(0);
  });
});

describe("the actions a message offers", () => {
  // "Mark as unread" was offered on a message the user sent themselves and on
  // their own half-written draft. Nothing reads those, so the flag means
  // nothing there and the button only invites a pointless round trip.
  it("does not offer mark-as-unread in Sent or Drafts", () => {
    expect(readerActions("sent").canMarkUnread).toBe(false);
    expect(readerActions("drafts").canMarkUnread).toBe(false);
  });

  it("offers mark-as-unread in the folders that receive mail", () => {
    expect(readerActions("inbox").canMarkUnread).toBe(true);
    expect(readerActions("archive").canMarkUnread).toBe(true);
  });

  // Reply and forward on your own unfinished draft make no sense either: the
  // thing to do with a draft is finish it.
  it("offers editing rather than replying on a draft", () => {
    const draft = readerActions("drafts");
    expect(draft.canEdit).toBe(true);
    expect(draft.canReply).toBe(false);
    expect(draft.canForward).toBe(false);
  });

  it("offers reply and forward on received mail", () => {
    const inbox = readerActions("inbox");
    expect(inbox.canReply).toBe(true);
    expect(inbox.canForward).toBe(true);
    expect(inbox.canEdit).toBe(false);
  });

  // Delete has to be offered everywhere: it was offered nowhere, which is why
  // a draft left behind by a sent message could not be removed at all.
  it("offers delete in every folder", () => {
    for (const folder of ["inbox", "sent", "drafts", "archive", "junk", "trash"]) {
      expect(readerActions(folder).canDelete).toBe(true);
    }
  });

  // In Trash the same button has to say it destroys the message, because there
  // it does - there is nowhere further to move it.
  it("says delete is permanent once in Trash", () => {
    expect(readerActions("trash").deleteIsPermanent).toBe(true);
    expect(readerActions("inbox").deleteIsPermanent).toBe(false);
  });
});

describe("where a message may be moved", () => {
  const folders = [
    { special_use: "inbox", name: "INBOX", unread_count: 0, total_count: 3 },
    { special_use: "sent", name: "Sent", unread_count: 0, total_count: 1 },
    { special_use: "drafts", name: "Drafts", unread_count: 0, total_count: 1 },
    { special_use: "archive", name: "Archive", unread_count: 0, total_count: 0 },
    { special_use: "junk", name: "Junk", unread_count: 0, total_count: 0 },
    { special_use: "trash", name: "Trash", unread_count: 0, total_count: 0 },
    { special_use: "", name: "Reports", unread_count: 0, total_count: 2 },
    { special_use: "", name: "Faturas", unread_count: 0, total_count: 0 },
  ];

  // Sent and Drafts record how a message came to exist, not where it was
  // filed. Offering them would let Sent claim a message it never sent.
  it("never offers Sent or Drafts", () => {
    const names = moveTargets(folders, "inbox").map((f) => f.name);
    expect(names).not.toContain("Sent");
    expect(names).not.toContain("Drafts");
  });

  it("does not offer the folder the message is already in", () => {
    expect(moveTargets(folders, "inbox").map((f) => f.name)).not.toContain("INBOX");
    expect(moveTargets(folders, "archive").map((f) => f.name)).not.toContain("Archive");
  });

  it("offers the ordinary destinations", () => {
    const names = moveTargets(folders, "inbox").map((f) => f.name);
    expect(names).toContain("Archive");
    expect(names).toContain("Junk");
    expect(names).toContain("Trash");
  });

  // A folder the user made themselves has no special-use role, and matching
  // only on roles would leave it unreachable.
  it("offers folders the user created", () => {
    const names = moveTargets(folders, "inbox").map((f) => f.name);
    expect(names).toContain("Reports");
    expect(names).toContain("Faturas");
  });

  it("can move out of Drafts and Sent, even though nothing moves in", () => {
    expect(moveTargets(folders, "drafts").map((f) => f.name)).toContain("Archive");
    expect(moveTargets(folders, "sent").map((f) => f.name)).toContain("Archive");
  });
});

describe("the folders a user made themselves", () => {
  const folders = [
    { special_use: "inbox", name: "INBOX", unread_count: 0, total_count: 3 },
    { special_use: "sent", name: "Sent", unread_count: 0, total_count: 1 },
    { special_use: "trash", name: "Trash", unread_count: 0, total_count: 0 },
    { special_use: "", name: "Reports", unread_count: 0, total_count: 2 },
    { special_use: "", name: "Faturas", unread_count: 1, total_count: 4 },
    { special_use: "", name: "Clientes", unread_count: 0, total_count: 0 },
  ];

  // The sidebar lists the standard folders from a fixed array, so a folder the
  // user created appears nowhere unless it is picked out separately.
  it("are the ones with no special-use role, minus the system's own", () => {
    const names = customFolders(folders).map((f) => f.name);
    expect(names).toEqual(["Clientes", "Faturas"]);
  });

  it("leaves out Reports, which this server fills itself", () => {
    expect(customFolders(folders).map((f) => f.name)).not.toContain("Reports");
  });

  it("lists them in alphabetical order, since nothing else orders them", () => {
    expect(customFolders(folders).map((f) => f.name)).toEqual(["Clientes", "Faturas"]);
  });

  it("copes with no folders at all", () => {
    expect(customFolders([])).toEqual([]);
    expect(customFolders(undefined)).toEqual([]);
  });
});

describe("move targets when there is no current folder", () => {
  const folders = [
    { special_use: "inbox", name: "INBOX", unread_count: 0, total_count: 1 },
    { special_use: "sent", name: "Sent", unread_count: 0, total_count: 0 },
    { special_use: "", name: "Faturas", unread_count: 0, total_count: 0 },
    { special_use: "", name: "Reports", unread_count: 0, total_count: 0 },
  ];

  // The rules screen picks a destination without any message being open, so it
  // has no "current folder" to exclude. Passing an empty string made
  // `role === current` true for every folder with no special-use role - which
  // is exactly the folders the user created - so the destination list silently
  // left out the only folders a filing rule is usually written for.
  it("offers the custom folders when no folder is excluded", () => {
    const names = moveTargets(folders, "").map((f) => f.name);
    expect(names).toContain("Faturas");
    expect(names).toContain("Reports");
    expect(names).toContain("INBOX");
  });

  it("still leaves Sent out", () => {
    expect(moveTargets(folders, "").map((f) => f.name)).not.toContain("Sent");
  });
});

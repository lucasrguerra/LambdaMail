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

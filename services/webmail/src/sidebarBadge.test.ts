import { describe, it, expect } from "vitest";
import { folderBadge, type FolderSummary } from "./lib/mailCounts";

/**
 * What the rail actually puts on screen, folder by folder.
 *
 * The component renders the unread count only when showsUnread is true, so
 * this is the rendered result: a number, or nothing at all. Written as a table
 * because the point of the change is what the rail looks like as a whole, not
 * what one folder returns.
 */
function rendered(folders: FolderSummary[], route: string): string {
  const badge = folderBadge(folders, route);
  return badge.showsUnread ? String(badge.unread) : "";
}

describe("what each folder shows in the rail", () => {
  const folders: FolderSummary[] = [
    { special_use: "inbox", name: "INBOX", unread_count: 3, total_count: 17 },
    { special_use: "sent", name: "Sent", unread_count: 0, total_count: 2 },
    { special_use: "drafts", name: "Drafts", unread_count: 0, total_count: 1 },
    { special_use: "junk", name: "Junk", unread_count: 0, total_count: 0 },
    { special_use: "", name: "Faturas", unread_count: 5, total_count: 9 },
  ];

  it("shows only the unread count, never the size", () => {
    expect(rendered(folders, "inbox")).toBe("3");
    expect(rendered(folders, "inbox")).not.toContain("17");
  });

  it("shows nothing for folders with nothing unread", () => {
    expect(rendered(folders, "sent")).toBe("");
    expect(rendered(folders, "drafts")).toBe("");
    expect(rendered(folders, "junk")).toBe("");
  });

  it("shows the unread count on a folder the user created", () => {
    expect(rendered(folders, "Faturas")).toBe("5");
  });

  // The whole rail, as a reader sees it: a number only where mail is waiting.
  it("leaves the rail carrying one number per folder with unread mail", () => {
    const all = ["inbox", "sent", "drafts", "junk", "Faturas"].map((r) => rendered(folders, r));
    expect(all.filter((v) => v !== "")).toEqual(["3", "5"]);
  });

  it("is completely bare when everything has been read", () => {
    const read = folders.map((f) => ({ ...f, unread_count: 0 }));
    const all = ["inbox", "sent", "drafts", "junk", "Faturas"].map((r) => rendered(read, r));
    expect(all.every((v) => v === "")).toBe(true);
  });
});

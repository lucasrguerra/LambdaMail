import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * The composer's draft handling, and the delete action the mail list was
 * missing entirely.
 *
 * Every check here is a defect the user hit: a message sent from the composer
 * left its autosaved draft behind, that draft was empty because the autosave
 * never saw the body, and no screen offered any way to remove it.
 */

const read = (path: string) => readFileSync(resolve(process.cwd(), path), "utf8");
const code = (path: string) =>
  read(path)
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, "")
    .replace(/^\s*\/\/.*$/gm, "");

const COMPOSE = "src/app/(user)/user/compose/page.tsx";
const MAIL_PAGE = "src/app/(user)/user/mail/[folder]/page.tsx";

describe("the draft a sent message leaves behind", () => {
  // Sending posted to /mail/send without ever naming the draft it came from,
  // so the draft stayed in Drafts as a duplicate of mail already on its way.
  it("tells the server which draft the sent message came from", () => {
    const compose = code(COMPOSE);
    expect(compose).toMatch(/draft_uid:\s*draftUidRef\.current/);
  });

  it("posts the draft UID on the send request, not only on the autosave", () => {
    const compose = code(COMPOSE);
    const sendCall = compose.slice(compose.indexOf('"/api/v1/mail/send"'));
    expect(sendCall.slice(0, 600)).toContain("draft_uid");
  });
});

describe("what the autosave actually stores", () => {
  // The body lived in a contenteditable read through a ref, and the debounce
  // depended only on the header fields. Typing a message therefore never
  // re-triggered a save, so the stored draft held the recipient and nothing
  // else - the empty draft the user could see and could not delete.
  it("re-runs when the body changes, not only the recipient and subject", () => {
    const compose = code(COMPOSE);
    const effect = compose.slice(compose.indexOf("const timer = setTimeout(() => void saveDraft()"));
    const deps = effect.slice(effect.indexOf("}, ["), effect.indexOf("]);") + 3);
    expect(deps).toContain("body");
  });

  it("keeps the body in state so a change can be observed at all", () => {
    const compose = code(COMPOSE);
    expect(compose).toMatch(/useState.*\n?.*body|const \[body, setBody\]/);
    expect(compose).toContain("setBody");
  });

  it("does not save a draft that has no content anywhere", () => {
    const compose = code(COMPOSE);
    expect(compose).toMatch(/if \(!to && !subject && !body/);
  });
});

describe("deleting a message", () => {
  // There was no delete anywhere in the webmail: no button, and no route
  // behind one. Anything the user wanted rid of stayed forever.
  it("offers a delete action on the message list", () => {
    const page = code(MAIL_PAGE);
    expect(page).toContain("/api/v1/mail/delete");
  });

  it("refreshes the counters after deleting, so the badges follow", () => {
    const page = code(MAIL_PAGE);
    const del = page.slice(page.indexOf('"/api/v1/mail/delete"'));
    expect(del.slice(0, 900)).toContain("notifyMailStateChanged");
  });

  it("removes the deleted row from the list without a full reload", () => {
    const page = code(MAIL_PAGE);
    const del = page.slice(page.indexOf("const deleteMessage"));
    expect(del.slice(0, 1200)).toMatch(/setMessages\(/);
  });
});

describe("the Reports folder", () => {
  // DMARC and TLS-RPT reports are parsed on arrival and filed here instead of
  // the inbox. The sidebar is a fixed list, so without an entry the folder
  // exists and receives mail that no screen can reach.
  it("is reachable from the sidebar", () => {
    const layout = code("src/app/(user)/user/layout.tsx");
    expect(layout).toContain("/user/mail/reports");
  });

  it("is named through the translator, not hardcoded", () => {
    const layout = code("src/app/(user)/user/layout.tsx");
    expect(layout).toMatch(/t\("mail\.reports"\)/);
  });

  it("has a title on the message list too", () => {
    const page = code(MAIL_PAGE);
    expect(page).toMatch(/reports:\s*t\("mail\.reports"\)/);
  });
});

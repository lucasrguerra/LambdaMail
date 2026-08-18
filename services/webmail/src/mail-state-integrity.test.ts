import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * The wiring behind the counters, the reader pane and the two surfaces.
 *
 * mailCounts.test.ts and emailBody.test.ts cover the rules themselves; these
 * check that the screens actually go through them, because every defect in this
 * file was a component computing the number or the markup inline where nothing
 * could reach it.
 */

const read = (path: string) => readFileSync(resolve(process.cwd(), path), "utf8");
/** Strips comments, so a fix may name the code it removed without tripping. */
const code = (path: string) =>
  read(path)
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, "")
    .replace(/^\s*\/\/.*$/gm, "");

const USER_LAYOUT = "src/app/(user)/user/layout.tsx";
const MAIL_PAGE = "src/app/(user)/user/mail/[folder]/page.tsx";
const ADMIN_LOGIN = "src/app/(auth)/admin/login/page.tsx";
const USER_LOGIN = "src/app/(auth)/user/login/page.tsx";

describe("the counters the sidebar shows", () => {
  // The badge was computed inline from folders.find(...).unread_count, which
  // missed folders with no special-use role and printed nothing for Drafts.
  it("goes through the tested counting rules", () => {
    expect(code(USER_LAYOUT)).toContain("badgeCount");
    expect(code(USER_LAYOUT)).not.toMatch(/unread_count\s*\?\?/);
  });

  // The folder list was fetched once on mount, so every badge froze at the
  // value it had when the tab was opened: new mail did not raise it and reading
  // a message did not lower it.
  it("refreshes the folder list instead of fetching it once on mount", () => {
    const layout = code(USER_LAYOUT);
    expect(layout).toContain("useFolders");
    expect(layout).not.toMatch(/useEffect\([^)]*\)\s*=>\s*\{\s*void fetch\("\/api\/v1\/mail\/folders"\)/);
  });

  it("re-reads the counters when the server pushes a mailbox event", () => {
    expect(code("src/lib/useFolders.ts")).toContain("useMailEvents");
  });

  // Reading a message changes a counter that lives in another component, so the
  // reader has to say so; nothing else can know.
  it("gives the reader a way to announce that a counter moved", () => {
    expect(code("src/lib/useFolders.ts")).toContain("MAIL_STATE_CHANGED");
    expect(code(MAIL_PAGE)).toContain("notifyMailStateChanged");
  });
});

describe("the counters the message list shows", () => {
  // The header printed messages.length - the size of one page, capped at 50 -
  // as though it were the size of the folder.
  it("does not present the length of the loaded page as the folder total", () => {
    const page = code(MAIL_PAGE);
    expect(page).toContain("listHeaderCount");
    expect(page).not.toContain("({messages.length})");
  });

  it("filters through the tested rule rather than a chain of inline predicates", () => {
    expect(code(MAIL_PAGE)).toContain("filterMessages");
  });

  // A chip that filters by unread or by attachment is worth a number, and both
  // were available on the page already.
  it("shows how many messages each filter chip would keep", () => {
    expect(code(MAIL_PAGE)).toContain("messageCounts");
  });
});

describe("read and unread state", () => {
  // Opening a message marked it read on the server, so the flag flipping back
  // on the next reload was the list, the badge and the server disagreeing.
  it("persists a read-state change through the API, not only in local state", () => {
    expect(code(MAIL_PAGE)).toContain("/api/v1/mail/seen");
  });

  it("keeps the row in step through the tested helper", () => {
    expect(code(MAIL_PAGE)).toContain("applySeen");
  });
});

describe("the reader pane", () => {
  it("resolves the message's own inline images", () => {
    const page = code(MAIL_PAGE);
    expect(page).toContain("resolveInlineImages");
    expect(page).toContain("inline_images");
  });

  it("renders the body inside the tested document rather than a bare fragment", () => {
    expect(code(MAIL_PAGE)).toContain("buildReaderDocument");
  });

  // A fixed 500px frame clipped every long message and left a slab of white
  // under every short one.
  it("sizes the frame from the message instead of pinning it to 500px", () => {
    const page = code(MAIL_PAGE);
    expect(page).not.toContain("h-[500px]");
    expect(page).toContain("lm:reader-height");
    // The height report needs script inside the frame, which sandbox="" forbids.
    expect(page).toContain("allow-scripts");
  });

  // Every one of these was a Portuguese string typed into a component that had
  // a translated bundle beside it, so the interface was half English for a
  // Spanish reader and half Portuguese for an English one.
  it("has no hardcoded strings left in the reader", () => {
    const page = read(MAIL_PAGE);
    for (const literal of ["Sem assunto", "Para:", "Imagens Habilitadas", "(sanitised)"]) {
      expect(page).not.toContain(literal);
    }
  });
});

describe("moving between the two surfaces", () => {
  // The mailbox is the account's own mail behind the account's own password.
  it("does not ask the webmail sign-in for a second factor", () => {
    const login = code(USER_LOGIN);
    expect(login).not.toContain("mfa_required");
    expect(login).not.toContain("challenge_token");
  });

  // Crossing into the console asked for the password again, though the session
  // in hand had just proven it.
  it("steps up into the console instead of sending the operator to a password form", () => {
    expect(code(USER_LAYOUT)).toContain("/admin/step-up");
    expect(code(USER_LAYOUT)).not.toContain('href="/admin/login"');
  });

  it("collects only the second factor on the step-up screen", () => {
    const stepUp = code("src/app/(auth)/admin/step-up/page.tsx");
    expect(stepUp).toContain("/api/v1/auth/admin/step-up");
    expect(stepUp).not.toContain('type="password"');
  });

  // Leaving the console needs nothing: the admin sign-in now issues the webmail
  // session too, so the link back cannot land on a login screen.
  it("keeps a plain link out of the console", () => {
    expect(code("src/app/(admin)/admin/layout.tsx")).toContain("/user/mail/inbox");
  });

  it("still requires the second factor at the admin sign-in itself", () => {
    expect(code(ADMIN_LOGIN)).toContain("challenge_token");
  });
});

import { describe, it, expect } from "vitest";
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { MESSAGES, translate } from "./i18n/config.js";

/**
 * Guards the class of defect this interface kept shipping: text that cannot be
 * translated, and screens filled with invented data.
 *
 * Each of these reproduces something that reached production. They are file
 * assertions rather than rendering tests because that is where the defects
 * lived - a constant at the top of a module, a placeholder never substituted.
 */

function sourceFiles(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(dir)) {
      const path = join(dir, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (path.endsWith(".tsx") || (path.endsWith(".ts") && !path.includes(".test."))) out.push(path);
    }
  };
  walk(resolve(process.cwd(), "src"));
  return out;
}

describe("message interpolation", () => {
  // The compose screen rendered "Undo Send ({seconds}s remaining) (9s
  // remaining)": the bundle's placeholder was never filled, and a second
  // hardcoded countdown was appended after it.
  it("substitutes placeholders instead of printing them", () => {
    const out = translate(MESSAGES.en, "mail.undoSend", { seconds: 9 });
    expect(out).toContain("9");
    expect(out).not.toContain("{seconds}");
  });

  it("substitutes in every locale, not just the fallback", () => {
    for (const locale of ["en", "pt-BR", "es"] as const) {
      expect(translate(MESSAGES[locale], "mail.undoSend", { seconds: 3 })).not.toContain("{");
    }
  });

  // Leaving the token visible is a bug report; blanking it hides the bug.
  it("leaves an unknown placeholder alone rather than blanking it", () => {
    expect(translate(MESSAGES.en, "mail.undoSend", {})).toContain("{seconds}");
  });

  it("still resolves keys that take no parameters", () => {
    expect(translate(MESSAGES["pt-BR"], "auth.signInButton")).toBe("Entrar");
  });
});

describe("the Portuguese bundle is written in Portuguese", () => {
  // A pass that stripped diacritics to satisfy the ASCII-source rule was
  // applied to the JSON bundles as well, so the console displayed Portuguese
  // words with their accents missing. That rule covers source code; a
  // translation is data, and stripping it there just makes the text wrong.
  //
  // The expected values are written as \u escapes for the same reason: this
  // file is source, so it may name the accented words without containing them.
  it("keeps diacritics on words that require them", () => {
    const pt = MESSAGES["pt-BR"];
    expect(pt.ui.recipient).toBe("Destinat\u00e1rio");
    expect(pt.ui.lastError).toBe("\u00daltimo erro");
    expect(pt.ui.actions).toBe("A\u00e7\u00f5es");
  });

  it("has accented text across the bundle, not just in a few entries", () => {
    const values = Object.values(MESSAGES["pt-BR"]).flatMap((ns) => Object.values(ns));
    const accented = values.filter((v) => /[^\u0000-\u007F]/.test(v));
    expect(accented.length).toBeGreaterThan(50);
  });
});

/**
 * Strips comments so these checks read the code, not the prose about it.
 * Several of the fixes below are documented by naming the value they removed,
 * and a scan that could not tell the two apart would forbid explaining itself.
 */
function code(file: string): string {
  return readFileSync(file, "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "");
}

describe("no fabricated data is baked into the interface", () => {
  const files = sourceFiles();
  const read = (f: string) => readFileSync(f, "utf8");

  // The queue screen shipped two invented jobs whose Retry and Freeze buttons
  // only edited local state; the sidebars showed a fixed address; the domains
  // screen showed thirteen example.com records marked VERIFIED.
  const forbidden = [
    "job-101",
    "job-102",
    "partner@remotehost.com",
    "alerts@monitoring.org",
    "admin@lambdamail.local",
    "user@lambdamail.local",
    "default-domain",
  ];

  for (const needle of forbidden) {
    it(`does not contain the placeholder "${needle}"`, () => {
      const offenders = files.filter((f) => code(f).includes(needle));
      expect(offenders).toEqual([]);
    });
  }

  it("reads the outbound queue from the API rather than component state", () => {
    const queue = read(resolve(process.cwd(), "src/app/(admin)/admin/queue/page.tsx"));
    expect(queue).toContain("/api/v1/admin/queue");
  });

  // Every KPI fell back to an invented constant, so an unreachable API showed
  // a dashboard of plausible traffic for a server that had delivered nothing.
  it("does not fall back to invented dashboard figures", () => {
    const dash = code(resolve(process.cwd(), "src/app/(admin)/admin/dashboard/page.tsx"));
    expect(dash).not.toMatch(/\?\?\s*\d{2,}/);
  });
});

describe("surface routing", () => {
  // The root listed the two surfaces side by side and advertised cookie names
  // and audiences to anonymous visitors.
  it("has no surface-selector landing page", () => {
    const page = code(resolve(process.cwd(), "src/app/page.tsx"));
    expect(page).toContain("redirect");
    expect(page).toContain("/user/mail/inbox");
    expect(page).not.toMatch(/Access Admin Console|Surface Selector/);
  });

  it("leaves no links pointing at the removed selector", () => {
    for (const file of sourceFiles()) {
      expect(code(file)).not.toMatch(/Back to Surface Selector|Back to surface selection/);
    }
  });

  // The destination is the step-up rather than the admin sign-in: the console
  // costs a second factor, but not the password the session in hand just
  // proved. mail-state-integrity.test.ts covers the rest of that crossing.
  it("offers the console from inside the app, gated on the account's role", () => {
    const layout = readFileSync(resolve(process.cwd(), "src/app/(user)/user/layout.tsx"), "utf8");
    expect(layout).toContain("isAdminRole");
    expect(layout).toContain("/admin/step-up");
  });

  it("offers a plain way back to webmail from the console", () => {
    const layout = readFileSync(resolve(process.cwd(), "src/app/(admin)/admin/layout.tsx"), "utf8");
    expect(layout).toContain("/user/mail/inbox");
  });
});

describe("two-factor enrolment", () => {
  it("ships a QR code component both enrolment screens use", () => {
    const path = resolve(process.cwd(), "src/components/TotpEnrolment.tsx");
    expect(existsSync(path)).toBe(true);
    expect(readFileSync(path, "utf8")).toContain("qrcode-generator");

    for (const screen of [
      "src/app/(user)/user/settings/page.tsx",
      "src/app/(auth)/admin/login/page.tsx",
    ]) {
      expect(readFileSync(resolve(process.cwd(), screen), "utf8")).toContain("TotpEnrolment");
    }
  });

  // The recovery-code step rendered its own "sign in" button while the form's
  // submit button stayed visible below it: two identical buttons, and no way
  // to copy the codes it told you to save.
  it("gives the recovery-code step a copy action and a single continue", () => {
    const source = readFileSync(resolve(process.cwd(), "src/components/TotpEnrolment.tsx"), "utf8");
    expect(source).toContain("common.copy");
    expect(source).toContain("onContinue");
  });
});

describe("API proxy routing", () => {
  // A route added to the protocols service but not to the proxy's list is sent
  // to the auth service instead, which answers 401 from its admin guard. That
  // is indistinguishable from a session problem, so the failure looks like
  // "you are not logged in" rather than "this was routed to the wrong
  // service" - which is exactly how it went unnoticed once already.
  it("sends every protocols-owned admin area to the protocols service", () => {
    const proxy = readFileSync(resolve(process.cwd(), "src/app/api/v1/[...path]/route.ts"), "utf8");

    // Areas the protocols service actually serves, taken from its router.
    for (const area of ["dkim", "tls", "dns"]) {
      expect(proxy).toContain(`"${area}"`);
    }
    expect(proxy).toMatch(/PROTOCOLS_ADMIN_AREAS/);
  });

  it("still sends mail to protocols and everything else to auth", () => {
    const proxy = readFileSync(resolve(process.cwd(), "src/app/api/v1/[...path]/route.ts"), "utf8");
    expect(proxy).toMatch(/path\[0\] === "mail".*PROTOCOLS_SERVICE_URL/s);
    expect(proxy).toContain("return AUTH_SERVICE_URL");
  });
});

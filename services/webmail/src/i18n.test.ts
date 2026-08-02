import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

function loadJson(filename: string): Record<string, unknown> {
  const path = resolve(process.cwd(), "messages", filename);
  return JSON.parse(readFileSync(path, "utf8"));
}

function getLeafKeys(obj: Record<string, unknown>, prefix = ""): string[] {
  let keys: string[] = [];
  for (const key of Object.keys(obj)) {
    const val = obj[key];
    const fullKey = prefix ? `${prefix}.${key}` : key;
    if (typeof val === "object" && val !== null && !Array.isArray(val)) {
      keys = keys.concat(getLeafKeys(val as Record<string, unknown>, fullKey));
    } else {
      keys.push(fullKey);
    }
  }
  return keys.sort();
}

describe("i18n message bundles", () => {
  const en = loadJson("en.json");
  const ptBR = loadJson("pt-BR.json");
  const es = loadJson("es.json");

  const enKeys = getLeafKeys(en);
  const ptKeys = getLeafKeys(ptBR);
  const esKeys = getLeafKeys(es);

  it("has exact key parity between en.json (source of truth) and pt-BR.json", () => {
    expect(ptKeys).toEqual(enKeys);
  });

  it("has exact key parity between en.json (source of truth) and es.json", () => {
    expect(esKeys).toEqual(enKeys);
  });
});

// Key parity alone passed while the interface was hardcoded English and the
// bundles were never imported. These tests check the wiring instead.
describe("i18n is actually wired into the app", () => {
  it("resolves a key through the same helper the components use", async () => {
    const { MESSAGES, translate } = await import("./i18n/config.js");
    expect(translate(MESSAGES["pt-BR"], "auth.signInButton")).not.toBe("auth.signInButton");
    expect(translate(MESSAGES["pt-BR"], "auth.signInButton")).not.toBe(
      translate(MESSAGES.en, "auth.signInButton"),
    );
  });

  it("falls back to the key rather than throwing on an unknown lookup", async () => {
    const { MESSAGES, translate } = await import("./i18n/config.js");
    expect(translate(MESSAGES.en, "nope.missing")).toBe("nope.missing");
  });

  it("negotiates a locale from Accept-Language, honouring q-values and bare tags", async () => {
    const { negotiateLocale } = await import("./i18n/config.js");
    expect(negotiateLocale("pt-BR,pt;q=0.9,en;q=0.8")).toBe("pt-BR");
    expect(negotiateLocale("pt")).toBe("pt-BR");
    expect(negotiateLocale("es-AR,es;q=0.9")).toBe("es");
    expect(negotiateLocale("en-US")).toBe("en");
    expect(negotiateLocale("de-DE")).toBe("en");
    expect(negotiateLocale(null)).toBe("en");
    // A low-quality preferred tag must not beat a higher-quality supported one.
    expect(negotiateLocale("de;q=1.0,es;q=0.9")).toBe("es");
  });

  it("keeps every UI string reachable: the layouts import the provider", async () => {
    const { readFileSync } = await import("node:fs");
    for (const file of [
      "src/app/layout.tsx",
      "src/app/(user)/user/layout.tsx",
      "src/app/(admin)/admin/layout.tsx",
    ]) {
      expect(readFileSync(file, "utf8")).toMatch(/i18n\/(provider|config)/);
    }
  });
});

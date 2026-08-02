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

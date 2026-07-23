#!/usr/bin/env node
// Fails the build if tracked source contains non-ASCII characters or common
// Portuguese terms in identifiers/comments (PLAN.md section 21.1: "all code in
// English, no exceptions - enforced by CI, not by human review").
import { execSync } from "node:child_process";
import { readFileSync } from "node:fs";

const BANNED_TERMS = [
  "usuario", "senha", "endereco", "mensagem", "dominio", "caixa",
  "remetente", "destinatario", "cadastro", "autenticacao",
];

const SCANNED_PREFIXES = ["services/", "migrations/", "scripts/"];
const SCANNED_EXTENSIONS = [".go", ".ts", ".tsx", ".sql", ".mjs", ".js"];

function trackedFiles() {
  return execSync("git ls-files", { encoding: "utf8" })
    .split("\n")
    .filter(Boolean)
    .filter((f) => SCANNED_PREFIXES.some((p) => f.startsWith(p)))
    .filter((f) => SCANNED_EXTENSIONS.some((ext) => f.endsWith(ext))); // tests are scanned too - same rule applies
}

let failures = [];

for (const file of trackedFiles()) {
  const isSelf = file === "scripts/lang-lint.mjs";
  const content = readFileSync(file, "utf8");
  const lines = content.split("\n");
  lines.forEach((line, i) => {
    if (/[^\x00-\x7F]/.test(line)) {
      failures.push(`${file}:${i + 1}: non-ASCII character`);
    }
    if (!isSelf) {
      const lower = line.toLowerCase();
      for (const term of BANNED_TERMS) {
        if (lower.includes(term)) {
          failures.push(`${file}:${i + 1}: banned Portuguese term "${term}"`);
        }
      }
    }
  });
}

if (failures.length > 0) {
  console.error(`lang-lint: ${failures.length} violation(s) found:\n`);
  for (const f of failures.slice(0, 50)) console.error(`  ${f}`);
  if (failures.length > 50) console.error(`  ... and ${failures.length - 50} more`);
  process.exit(1);
}

console.log("lang-lint: clean");

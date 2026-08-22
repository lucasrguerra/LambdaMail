/**
 * Rules as a person describes them, and the Sieve the server runs.
 *
 * The old screen asked which Sieve match type to use - :contains, :is,
 * :matches - and pasted the typed value straight into a script. That put a
 * pattern language in front of someone whose actual thought was "mail from the
 * bank goes in Faturas", and it produced a script that did not parse the
 * moment a value contained a quote, which silently disabled every rule at once.
 *
 * So the vocabulary here is the user's: a field, a plain-language condition, a
 * value and an action. Wildcards are generated from "starts with" and "ends
 * with"; nobody types one, and one typed by accident stays a literal.
 */

export type RuleField = "from" | "to" | "cc" | "subject";
export type RuleCondition = "contains" | "is" | "startsWith" | "endsWith" | "notContains";
export type RuleAction = "move" | "delete" | "markRead" | "flag";

export interface Rule {
  id: string;
  field: RuleField;
  condition: RuleCondition;
  value: string;
  action: RuleAction;
  /** The destination folder, for the "move" action. */
  target: string;
}

const HEADERS: Record<RuleField, string> = {
  from: "From",
  to: "To",
  cc: "Cc",
  subject: "Subject",
};

/**
 * Escapes a value for a Sieve string.
 *
 * Backslash first, or the backslashes added for the quotes would themselves be
 * escaped a second time. A value carrying a single quote used to end the
 * string early and break the whole script.
 */
function quote(value: string): string {
  return `"${value.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

/**
 * Escapes the characters Sieve's :matches treats as wildcards.
 *
 * Needed because the wildcard is ours, not the user's: someone writing
 * "50% * desconto" means an asterisk, and it must not become "any run of
 * characters" just because the surrounding condition happens to use :matches.
 */
function escapeWildcards(value: string): string {
  return value.replace(/([*?\\])/g, "\\$1");
}

/** The Sieve test for one rule, without the surrounding if. */
function testFor(rule: Rule): string {
  const header = quote(HEADERS[rule.field] ?? "Subject");

  switch (rule.condition) {
    case "is":
      return `header :is ${header} ${quote(rule.value)}`;
    case "startsWith":
      return `header :matches ${header} ${quote(escapeWildcards(rule.value) + "*")}`;
    case "endsWith":
      return `header :matches ${header} ${quote("*" + escapeWildcards(rule.value))}`;
    case "notContains":
      return `not header :contains ${header} ${quote(rule.value)}`;
    default:
      return `header :contains ${header} ${quote(rule.value)}`;
  }
}

function actionFor(rule: Rule): string {
  switch (rule.action) {
    case "delete":
      return "discard;";
    case "markRead":
      return `addflag "\\\\Seen";`;
    case "flag":
      return `addflag "\\\\Flagged";`;
    default:
      return `fileinto ${quote(rule.target || "INBOX")};`;
  }
}

/** The script for a list of rules, in the order they are listed. */
export function buildSieve(rules: Rule[]): string {
  return rules
    .filter((rule) => rule.value.trim() !== "")
    .map((rule) => `if ${testFor(rule)} { ${actionFor(rule)} }`)
    .join("\n");
}

// --- reading a script back -----------------------------------------------

/** One rule as this screen writes it, and nothing else. */
const RULE_LINE =
  /^if (not )?header :(contains|is|matches) "([^"]+)" "((?:[^"\\]|\\.)*)" \{ (fileinto "((?:[^"\\]|\\.)*)"|discard|addflag "\\\\(Seen|Flagged)");? \}$/;

function unquote(value: string): string {
  return value.replace(/\\(["\\])/g, "$1");
}

function unescapeWildcards(value: string): string {
  return value.replace(/\\([*?\\])/g, "$1");
}

/**
 * Reads a script back into rules, or returns null when it cannot.
 *
 * Null means "this screen cannot represent this script" - a hand-written one,
 * or something a desktop client wrote over ManageSieve. The screen then shows
 * the script rather than a simplified version of it, because rewriting
 * somebody's rules into the nearest thing this form can express would quietly
 * change what their mail does.
 */
export function parseSieve(script: string): Rule[] | null {
  const source = (script ?? "").trim();
  if (source === "") return [];

  const rules: Rule[] = [];
  const lines = source.split("\n").map((l) => l.trim()).filter((l) => l !== "");

  for (const [index, line] of lines.entries()) {
    const match = RULE_LINE.exec(line);
    if (!match) return null;

    const [, negated, comparator, header, rawValue, , rawFolder, flag] = match;
    const field = (Object.keys(HEADERS) as RuleField[]).find(
      (key) => HEADERS[key].toLowerCase() === header.toLowerCase(),
    );
    if (!field) return null;

    let value = unquote(rawValue);
    let condition: RuleCondition;
    if (negated) {
      condition = "notContains";
    } else if (comparator === "is") {
      condition = "is";
    } else if (comparator === "matches") {
      // Which end the generated wildcard sits on is what says whether the user
      // chose "starts with" or "ends with".
      if (value.endsWith("*") && !value.endsWith("\\*")) {
        condition = "startsWith";
        value = unescapeWildcards(value.slice(0, -1));
      } else if (value.startsWith("*")) {
        condition = "endsWith";
        value = unescapeWildcards(value.slice(1));
      } else {
        return null;
      }
    } else {
      condition = "contains";
    }

    let action: RuleAction = "move";
    let target = "";
    if (rawFolder !== undefined) {
      target = unquote(rawFolder);
    } else if (flag === "Seen") {
      action = "markRead";
    } else if (flag === "Flagged") {
      action = "flag";
    } else {
      action = "delete";
    }

    rules.push({ id: String(index + 1), field, condition, value, action, target });
  }

  return rules;
}

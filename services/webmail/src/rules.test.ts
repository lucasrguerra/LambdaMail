import { describe, it, expect } from "vitest";
import { buildSieve, parseSieve, type Rule } from "./lib/rules";

/**
 * Rules, as a person describes them, turned into Sieve and back.
 *
 * The old screen asked for a Sieve match type by name - :contains, :is,
 * :matches - and then pasted the value straight into a script. That put a
 * pattern language in front of someone who wanted to say "mail from the bank
 * goes in Faturas", and it broke outright on a value containing a quote.
 */

const rule = (over: Partial<Rule> = {}): Rule => ({
  id: "1",
  field: "subject",
  condition: "contains",
  value: "Fatura",
  action: "move",
  target: "Faturas",
  ...over,
});

describe("turning a rule into Sieve", () => {
  it("writes a filing rule the engine understands", () => {
    const script = buildSieve([rule()]);
    expect(script).toContain('if header :contains "Subject" "Fatura"');
    expect(script).toContain('fileinto "Faturas"');
  });

  // The wildcard is generated, never typed. "Starts with" is a thing a person
  // can mean; "^Fatura" is not.
  it("expresses starts-with as a wildcard the user never sees", () => {
    const script = buildSieve([rule({ condition: "startsWith", value: "Fatura" })]);
    expect(script).toContain(':matches "Subject" "Fatura*"');
  });

  it("expresses ends-with the same way", () => {
    const script = buildSieve([rule({ condition: "endsWith", value: ".pdf" })]);
    expect(script).toContain(':matches "Subject" "*.pdf"');
  });

  it("expresses is-exactly without wildcards", () => {
    const script = buildSieve([rule({ condition: "is", value: "Fatura" })]);
    expect(script).toContain(':is "Subject" "Fatura"');
  });

  it("expresses does-not-contain as a negated test", () => {
    const script = buildSieve([rule({ condition: "notContains", value: "spam" })]);
    expect(script).toContain('if not header :contains "Subject" "spam"');
  });

  // A value with a quote in it used to end the Sieve string early and produce
  // a script that did not parse - so one apostrophe silently disabled every
  // rule the user had.
  it("escapes a quote in the value", () => {
    const script = buildSieve([rule({ value: 'ele disse "olá"' })]);
    expect(script).toContain('\\"olá\\"');
    expect(parseSieve(script)!).toHaveLength(1);
  });

  it("escapes a backslash", () => {
    const script = buildSieve([rule({ value: "a\\b" })]);
    expect(parseSieve(script)![0].value).toBe("a\\b");
  });

  // A wildcard typed into a "contains" rule is a literal, not a pattern: the
  // user meant an asterisk.
  it("does not let a typed asterisk become a wildcard", () => {
    const script = buildSieve([rule({ condition: "contains", value: "50% * desconto" })]);
    expect(script).toContain(":contains");
    expect(script).not.toContain(":matches");
  });

  it("writes the other actions", () => {
    expect(buildSieve([rule({ action: "delete" })])).toContain("discard;");
    expect(buildSieve([rule({ action: "markRead" })])).toContain('addflag "\\\\Seen"');
    expect(buildSieve([rule({ action: "flag" })])).toContain('addflag "\\\\Flagged"');
  });

  it("maps every field to its real header", () => {
    expect(buildSieve([rule({ field: "from" })])).toContain('"From"');
    expect(buildSieve([rule({ field: "to" })])).toContain('"To"');
    expect(buildSieve([rule({ field: "cc" })])).toContain('"Cc"');
    expect(buildSieve([rule({ field: "subject" })])).toContain('"Subject"');
  });

  it("writes several rules in the order they are listed", () => {
    const script = buildSieve([
      rule({ id: "1", value: "Fatura", target: "Faturas" }),
      rule({ id: "2", value: "Convite", target: "Eventos" }),
    ]);
    expect(script.indexOf("Faturas")).toBeLessThan(script.indexOf("Eventos"));
  });

  it("produces an empty script for no rules", () => {
    expect(buildSieve([]).trim()).toBe("");
  });
});

describe("reading rules back out of Sieve", () => {
  it("round-trips every condition", () => {
    const conditions: Rule["condition"][] = ["contains", "is", "startsWith", "endsWith", "notContains"];
    for (const condition of conditions) {
      const original = rule({ condition, value: "Fatura" });
      const [read] = parseSieve(buildSieve([original]))!;
      expect(read.condition, condition).toBe(condition);
      expect(read.value, condition).toBe("Fatura");
    }
  });

  it("round-trips every action", () => {
    for (const action of ["move", "delete", "markRead", "flag"] as Rule["action"][]) {
      const [read] = parseSieve(buildSieve([rule({ action })]))!;
      expect(read.action, action).toBe(action);
    }
  });

  it("round-trips the field and the target folder", () => {
    const [read] = parseSieve(buildSieve([rule({ field: "from", target: "Relatórios" })]))!;
    expect(read.field).toBe("from");
    expect(read.target).toBe("Relatórios");
  });

  // A script written by hand, or by a desktop client over ManageSieve, may use
  // things this screen cannot express. It must be left alone rather than
  // silently rewritten into something simpler.
  it("reports a script it cannot represent instead of mangling it", () => {
    expect(parseSieve(`if anyof (header :contains "Subject" "a", header :is "From" "b") { fileinto "X"; }`))
      .toBeNull();
    expect(parseSieve(`require ["vacation"];\nvacation "fora";`)).toBeNull();
  });

  it("reads an empty script as no rules", () => {
    expect(parseSieve("")).toEqual([]);
    expect(parseSieve("   \n  ")).toEqual([]);
  });
});

describe("an asterisk the user typed", () => {
  // The dangerous case is not "contains" - it is a value containing an
  // asterisk in a condition that really does use :matches. There the typed
  // character sits in the same string as the generated wildcard, and if it is
  // not escaped it silently becomes "any run of characters": a rule meant for
  // subjects starting with "50% *" would match every subject starting with
  // "50% ".
  it("stays literal in starts-with", () => {
    const script = buildSieve([rule({ condition: "startsWith", value: "50% * desconto" })]);
    expect(script).toContain("50% \\\\* desconto*");
  });

  it("stays literal in ends-with", () => {
    const script = buildSieve([rule({ condition: "endsWith", value: "* fim" })]);
    expect(script).toContain("*\\\\* fim");
  });

  it("survives the round trip unchanged", () => {
    for (const condition of ["startsWith", "endsWith"] as Rule["condition"][]) {
      const [read] = parseSieve(buildSieve([rule({ condition, value: "50% * desconto" })]))!;
      expect(read.value, condition).toBe("50% * desconto");
      expect(read.condition, condition).toBe(condition);
    }
  });

  it("keeps a question mark literal too", () => {
    const [read] = parseSieve(buildSieve([rule({ condition: "startsWith", value: "Onde?" })]))!;
    expect(read.value).toBe("Onde?");
  });
});

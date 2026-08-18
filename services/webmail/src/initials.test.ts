import { describe, it, expect } from "vitest";
import { initialsFor } from "./lib/initials.js";

describe("avatar initials", () => {
  it("takes one letter from each of the first two words", () => {
    expect(initialsFor("ana.ribeiro@nortide.com.br")).toBe("AR");
    expect(initialsFor("marcos-cordeiro@transvale.com.br")).toBe("MC");
    expect(initialsFor("juliana_serrano@nortide.com.br")).toBe("JS");
  });

  it("takes two letters from a single-word local part", () => {
    expect(initialsFor("operacoes@nortide.com.br")).toBe("OP");
  });

  // The old avatar printed email[0], so every account at one domain collapsed
  // onto the same letter.
  it("distinguishes two accounts that share a first letter", () => {
    expect(initialsFor("ana.ribeiro@x.com")).not.toBe(initialsFor("ana.souza@x.com"));
  });

  it("stays a fixed-width mark when there is no address yet", () => {
    expect(initialsFor(undefined)).toBe("?");
    expect(initialsFor(null)).toBe("?");
    expect(initialsFor("")).toBe("?");
  });
});

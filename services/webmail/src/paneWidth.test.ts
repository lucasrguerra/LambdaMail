import { describe, expect, it } from "vitest";
import { clampListWidth, DEFAULT_LIST_WIDTH, MIN_LIST_WIDTH, MIN_READER_WIDTH } from "./lib/paneWidth";

describe("clampListWidth", () => {
  it("keeps a width that fits", () => {
    expect(clampListWidth(500, 1400)).toBe(500);
  });

  it("never lets the list shrink below its minimum", () => {
    expect(clampListWidth(50, 1400)).toBe(MIN_LIST_WIDTH);
  });

  it("always leaves the reader its minimum width", () => {
    expect(clampListWidth(5000, 1400)).toBe(1400 - MIN_READER_WIDTH);
  });

  it("holds the minimum on a window too narrow for both", () => {
    expect(clampListWidth(900, 600)).toBe(MIN_LIST_WIDTH);
  });

  it("treats infinity as the widest allowed", () => {
    expect(clampListWidth(Infinity, 1400)).toBe(1400 - MIN_READER_WIDTH);
  });

  it("falls back to the default for a value that is not a number", () => {
    expect(clampListWidth(NaN, 1400)).toBe(DEFAULT_LIST_WIDTH);
  });
});

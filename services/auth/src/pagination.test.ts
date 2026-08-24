import { describe, it, expect } from "vitest";
import { parsePageParams, MAX_PAGE_SIZE } from "./pagination.js";

const at = (qs: string) => new URL(`http://x/api${qs}`);

describe("pagination parameters", () => {
  it("defaults to the first page when nothing is asked for", () => {
    const p = parsePageParams(at(""));
    expect(p.page).toBe(1);
    expect(p.offset).toBe(0);
    expect(p.search).toBe("");
  });

  it("turns a page number into the right offset", () => {
    const p = parsePageParams(at("?page=3&page_size=25"));
    expect(p.pageSize).toBe(25);
    expect(p.offset).toBe(50);
  });

  // A page size taken at face value is a denial of service against your own
  // database: one request asking for ten million rows.
  it("caps the page size", () => {
    expect(parsePageParams(at("?page_size=100000")).pageSize).toBe(MAX_PAGE_SIZE);
  });

  // Anything that is not a sane positive integer must land on the default
  // rather than reaching SQL as NaN, a negative LIMIT, or a fractional OFFSET.
  it.each(["0", "-5", "abc", "1.5", "", "1e9", "Infinity", "NaN"])(
    "falls back to a safe page for page=%s",
    (value) => {
      const p = parsePageParams(at(`?page=${value}`));
      expect(Number.isInteger(p.page)).toBe(true);
      expect(p.page).toBeGreaterThanOrEqual(1);
      expect(Number.isInteger(p.offset)).toBe(true);
      expect(p.offset).toBeGreaterThanOrEqual(0);
    },
  );

  it.each(["0", "-5", "abc", "1.5", "NaN"])(
    "falls back to a safe page size for page_size=%s",
    (value) => {
      const p = parsePageParams(at(`?page_size=${value}`));
      expect(Number.isInteger(p.pageSize)).toBe(true);
      expect(p.pageSize).toBeGreaterThan(0);
      expect(p.pageSize).toBeLessThanOrEqual(MAX_PAGE_SIZE);
    },
  );

  it("trims the search term and keeps it as plain text", () => {
    // It reaches SQL as a bound parameter, so the wildcards a user types are
    // theirs to type; what must not happen is it arriving untrimmed and
    // matching nothing.
    expect(parsePageParams(at("?search=%20%20ana%20%20")).search).toBe("ana");
  });

  it("ignores an over-long search term instead of sending it to the database", () => {
    const p = parsePageParams(at(`?search=${"a".repeat(500)}`));
    expect(p.search.length).toBeLessThanOrEqual(200);
  });
});

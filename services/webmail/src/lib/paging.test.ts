import { describe, it, expect } from "vitest";
import { pageWindow, pageRange } from "./paging";

describe("pageWindow", () => {
  it("shows every page when there are few", () => {
    expect(pageWindow(1, 3)).toEqual([1, 2, 3]);
  });

  it("follows the current page without running past the ends", () => {
    expect(pageWindow(10, 20)).toEqual([8, 9, 10, 11, 12]);
    expect(pageWindow(1, 20)).toEqual([1, 2, 3]);
    expect(pageWindow(20, 20)).toEqual([18, 19, 20]);
  });

  // A page number out of range comes back from a stale URL or a list that
  // shrank under the reader; it must not produce an empty or negative pager.
  it.each([
    [0, 5],
    [-3, 5],
    [99, 5],
    [Number.NaN, 5],
  ])("stays inside the range for page=%s of %s", (page, total) => {
    const w = pageWindow(page, total);
    expect(w.length).toBeGreaterThan(0);
    for (const p of w) {
      expect(p).toBeGreaterThanOrEqual(1);
      expect(p).toBeLessThanOrEqual(total);
    }
  });

  it("survives having no pages at all", () => {
    expect(pageWindow(1, 0)).toEqual([1]);
  });
});

describe("pageRange", () => {
  it("describes the rows on this page", () => {
    expect(pageRange(1, 25, 100)).toEqual({ from: 1, to: 25 });
    expect(pageRange(4, 25, 100)).toEqual({ from: 76, to: 100 });
  });

  // The last page is usually short; saying "76 to 100 of 82" would be wrong.
  it("stops at the total on a partial last page", () => {
    expect(pageRange(4, 25, 82)).toEqual({ from: 76, to: 82 });
  });

  it("says nothing when there is nothing", () => {
    expect(pageRange(1, 25, 0)).toEqual({ from: 0, to: 0 });
  });
});

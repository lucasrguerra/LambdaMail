import { describe, it, expect } from "vitest";
import { formatBytes, usageRatio } from "./formatBytes";

describe("formatBytes", () => {
  // The case from the console: 140771 bytes rendered as "0 / 2 MB".
  it("shows a mailbox smaller than a megabyte as itself", () => {
    expect(formatBytes(140771)).toBe("137.5 KB");
  });

  it("climbs through the units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(2097152)).toBe("2.0 MB");
    expect(formatBytes(5_368_709_120)).toBe("5.0 GB");
  });

  it("keeps one decimal at every size, so the reading stays comparable", () => {
    expect(formatBytes(157_286_400)).toBe("150.0 MB");
  });

  it.each([-1, Number.NaN, Number.POSITIVE_INFINITY])("survives %s", (value) => {
    expect(typeof formatBytes(value)).toBe("string");
  });
});

describe("usageRatio", () => {
  it("measures from the raw bytes, not from rounded megabytes", () => {
    // Rounding first gave 0/2 = 0%, and the bar stayed empty on a mailbox
    // that was really 6.7% full.
    expect(usageRatio(140771, 2097152)).toBeCloseTo(0.0671, 3);
  });

  // Zero means unlimited; dividing by it reported every unlimited mailbox as
  // over its limit.
  it("never calls an unlimited mailbox full", () => {
    expect(usageRatio(5_000_000, 0)).toBe(0);
  });

  it("stops at full rather than overflowing the bar", () => {
    expect(usageRatio(3_000_000, 2_000_000)).toBe(1);
  });
});

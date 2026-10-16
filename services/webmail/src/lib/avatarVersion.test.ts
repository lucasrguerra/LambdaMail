import { describe, expect, it, vi } from "vitest";
import { avatarVersionOf, bumpAvatar, subscribeAvatarVersions } from "./avatarVersion";

describe("avatar versions", () => {
  it("starts at 0 for a photo nobody changed", () => {
    expect(avatarVersionOf("nobody@example.test")).toBe(0);
    expect(avatarVersionOf(null)).toBe(0);
  });

  it("changes after a bump, whatever the case of the address", () => {
    bumpAvatar("Ana@Example.test", 1000);
    expect(avatarVersionOf("ana@example.test")).toBe(1000);
    expect(avatarVersionOf("  ANA@example.test ")).toBe(1000);
  });

  it("never repeats a version, even when bumped in the same millisecond", () => {
    bumpAvatar("same@example.test", 5000);
    const first = avatarVersionOf("same@example.test");
    bumpAvatar("same@example.test", 5000);
    expect(avatarVersionOf("same@example.test")).toBeGreaterThan(first);
  });

  it("does not touch other people's photos", () => {
    bumpAvatar("one@example.test", 7000);
    expect(avatarVersionOf("two@example.test")).toBe(0);
  });

  it("tells subscribers, and stops when they unsubscribe", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeAvatarVersions(listener);
    bumpAvatar("watch@example.test", 9000);
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
    bumpAvatar("watch@example.test", 9001);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("ignores an empty address", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeAvatarVersions(listener);
    bumpAvatar("", 1);
    bumpAvatar(undefined, 1);
    expect(listener).not.toHaveBeenCalled();
    unsubscribe();
  });
});

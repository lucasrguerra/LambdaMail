import { describe, it, expect, beforeEach } from "vitest";
import {
  senderKey,
  isSenderTrusted,
  trustSender,
  revokeSender,
  TRUSTED_SENDERS_KEY,
} from "./lib/remoteImages";

/**
 * Whether a message's remote images load.
 *
 * The reader reset the choice every time a message was opened, so the same
 * newsletter had to be unblocked again on every single read - and the button
 * gave no way to say "stop asking me about this sender".
 *
 * Blocked stays the default: remote images are how a sender learns that a
 * message was opened, and when. Only an explicit decision changes it.
 */
beforeEach(() => localStorage.clear());

describe("identifying the sender", () => {
  it("reads the address out of a display-name form", () => {
    expect(senderKey('Jenny <jenny@example.test>')).toBe("jenny@example.test");
  });

  it("ignores case, so one decision covers the sender either way", () => {
    expect(senderKey("Jenny@Example.TEST")).toBe(senderKey("jenny@example.test"));
  });

  it("returns empty for something that is not an address", () => {
    expect(senderKey("")).toBe("");
    expect(senderKey("   ")).toBe("");
  });
});

describe("remembering the decision", () => {
  it("blocks by default, so opening a message never phones home on its own", () => {
    expect(isSenderTrusted("jenny@example.test")).toBe(false);
  });

  it("keeps loading images from a sender once allowed", () => {
    trustSender("Jenny <jenny@example.test>");
    expect(isSenderTrusted("jenny@example.test")).toBe(true);
    // And on a different message from the same sender.
    expect(isSenderTrusted("JENNY@EXAMPLE.TEST")).toBe(true);
  });

  it("can be taken back", () => {
    trustSender("jenny@example.test");
    revokeSender("jenny@example.test");
    expect(isSenderTrusted("jenny@example.test")).toBe(false);
  });

  it("keeps senders apart", () => {
    trustSender("jenny@example.test");
    expect(isSenderTrusted("someone@else.test")).toBe(false);
  });

  it("survives a reload, which is the whole point", () => {
    trustSender("jenny@example.test");
    const stored = localStorage.getItem(TRUSTED_SENDERS_KEY);
    expect(stored).toContain("jenny@example.test");
  });

  it("does not grow without bound as senders accumulate", () => {
    for (let i = 0; i < 500; i++) trustSender(`s${i}@example.test`);
    const stored = JSON.parse(localStorage.getItem(TRUSTED_SENDERS_KEY) ?? "[]");
    expect(stored.length).toBeLessThanOrEqual(200);
    // The most recent decision is the one that must survive the trim.
    expect(isSenderTrusted("s499@example.test")).toBe(true);
  });

  // A private window, cleared site data, or a browser that refuses storage:
  // the reader must still open, just without remembering.
  it("degrades to blocked when storage is unavailable", () => {
    const original = Object.getOwnPropertyDescriptor(window, "localStorage");
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      get() {
        throw new Error("storage disabled");
      },
    });
    expect(() => isSenderTrusted("jenny@example.test")).not.toThrow();
    expect(isSenderTrusted("jenny@example.test")).toBe(false);
    expect(() => trustSender("jenny@example.test")).not.toThrow();
    if (original) Object.defineProperty(window, "localStorage", original);
  });

  it("ignores corrupted storage rather than failing to render", () => {
    localStorage.setItem(TRUSTED_SENDERS_KEY, "{not json");
    expect(isSenderTrusted("jenny@example.test")).toBe(false);
  });
});

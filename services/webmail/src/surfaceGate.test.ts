import { describe, it, expect } from "vitest";
import { surfaceGateFor } from "./lib/surfaceGate.js";

/**
 * Which session, if any, a page needs before it may render.
 *
 * The gate used to be a pair of string comparisons inside the middleware, and
 * it had no way to express the case that matters most here: the step-up screen
 * lives under /admin but is reached with a webmail session, because obtaining
 * the admin one is what it is for. Gating it on an admin session made it
 * unreachable and bounced the operator to the password form it replaces.
 */

describe("the session a path requires", () => {
  it("guards the console with the admin session", () => {
    expect(surfaceGateFor("/admin/dashboard")).toBe("admin");
    expect(surfaceGateFor("/admin/domains")).toBe("admin");
  });

  it("guards the webmail with the webmail session", () => {
    expect(surfaceGateFor("/user/mail/inbox")).toBe("user");
    expect(surfaceGateFor("/user/settings")).toBe("user");
  });

  it("leaves both sign-in screens open", () => {
    expect(surfaceGateFor("/user/login")).toBe(null);
    expect(surfaceGateFor("/admin/login")).toBe(null);
  });

  // The whole point of the step-up is that the caller has a webmail session and
  // not yet an admin one.
  it("gates the step-up on the webmail session it steps up from", () => {
    expect(surfaceGateFor("/admin/step-up")).toBe("user");
  });

  it("leaves everything outside the two surfaces alone", () => {
    expect(surfaceGateFor("/")).toBe(null);
    expect(surfaceGateFor("/health")).toBe(null);
  });

  // "/userland" is not the webmail, and "/admin-x" is not the console. Matching
  // on a bare prefix would gate paths that have nothing to do with either.
  it("does not gate a path that merely starts with the same letters", () => {
    expect(surfaceGateFor("/userland")).toBe(null);
    expect(surfaceGateFor("/administration")).toBe(null);
  });
});

describe("where an ungated visitor is sent", () => {
  it("sends someone without a webmail session to the webmail sign-in", () => {
    expect(surfaceGateFor("/admin/step-up")).toBe("user");
  });
});

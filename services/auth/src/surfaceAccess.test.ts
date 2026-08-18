import { describe, it, expect } from "vitest";

process.env.JWT_SECRET ||= "a-test-signing-secret-long-enough-for-the-guard";

const { createJwt, verifyJwt, isSurfaceAuthorized } = await import("./session.js");
const { secondFactorRequired, surfacesGrantedBy, cookieNameFor, stepUpNeedsSecondFactor } =
  await import("./surfaceAccess.js");

/**
 * Which surface a login opens, and what it costs to cross between them.
 *
 * The rules used to be spread through the login handler and got two things
 * wrong that made the app tiresome to use. Reading mail demanded a second
 * factor, which the mailbox does not require - only the console does. And a
 * session issued at the admin sign-in granted the admin audience alone, so
 * leaving the console landed on the webmail's login screen even though the
 * operator had authenticated a moment earlier, with a stronger factor.
 */

describe("what each surface costs to open", () => {
  // The webmail is the account's own mail behind the account's own password.
  it("does not demand a second factor to read mail", () => {
    expect(secondFactorRequired("user")).toBe(false);
  });

  // The console changes other people's mailboxes, domains and DNS.
  it("always demands one for the admin console", () => {
    expect(secondFactorRequired("admin")).toBe(true);
  });
});

describe("what a session grants", () => {
  it("gives a webmail sign-in the webmail and nothing more", () => {
    expect(surfacesGrantedBy("user")).toEqual(["user"]);
  });

  // Proving a second factor is strictly more than the webmail asks for, so
  // being sent back to a password prompt on the way out of the console was
  // asking for something already provided.
  it("gives an admin sign-in the webmail as well, since it costs strictly more", () => {
    expect(surfacesGrantedBy("admin")).toEqual(["admin", "user"]);
  });

  it("names a separate cookie per surface, so neither overwrites the other", () => {
    expect(cookieNameFor("user")).toBe("lm_user_session");
    expect(cookieNameFor("admin")).toBe("lm_admin_session");
  });
});

describe("crossing from the webmail into the console", () => {
  const userSession = () =>
    createJwt({
      sub: "1", email: "a@b.test", role: "SUPER_ADMIN", domainId: "d",
      surface: "user", aud: "lambdamail:user", mfaSatisfied: false, purpose: "session",
    });

  // The point of the step-up: the password was already proven by the session in
  // hand, so only the factor the console adds is asked for.
  it("asks for the second factor and not for the password again", () => {
    const decision = stepUpNeedsSecondFactor(userSession());
    expect(decision).toBe(true);
  });

  it("refuses to step up from a challenge token, which proves only a password", () => {
    const challenge = createJwt({
      sub: "1", email: "a@b.test", role: "SUPER_ADMIN", domainId: "d",
      surface: "user", aud: "lambdamail:user", mfaSatisfied: false, purpose: "mfa_challenge",
    });
    expect(stepUpNeedsSecondFactor(challenge)).toBe(false);
  });

  it("refuses to step up from nothing at all", () => {
    expect(stepUpNeedsSecondFactor(null)).toBe(false);
    expect(stepUpNeedsSecondFactor("not-a-token")).toBe(false);
  });
});

describe("every issued session is its own session", () => {
  const claims = {
    sub: "1", email: "a@b.test", role: "USER" as const, domainId: "d",
    surface: "user" as const, aud: "lambdamail:user" as const,
    mfaSatisfied: false, purpose: "session" as const,
  };

  /**
   * Signing in twice in the same second used to mint the identical token.
   *
   * Nothing in the payload varied below one second - iat and exp are in
   * seconds - so two sessions hashed to the same value and the second insert
   * hit the unique index on web_sessions.refresh_token_hash. The user saw a
   * 500 from a correct password, which is what a double-clicked sign-in does.
   */
  it("mints a different token for two sign-ins in the same second", () => {
    expect(createJwt(claims)).not.toBe(createJwt(claims));
  });

  // The two audiences an admin sign-in issues are recorded as separate rows,
  // so they must not collide with each other either.
  it("mints a different token per surface within one sign-in", () => {
    const admin = createJwt({ ...claims, surface: "admin", aud: "lambdamail:admin" });
    expect(admin).not.toBe(createJwt(claims));
  });

  // The identity has to survive verification, or a revoked session could not
  // be told apart from a reissued one.
  it("carries a distinct identifier through verification", () => {
    const first = verifyJwt(createJwt(claims));
    const second = verifyJwt(createJwt(claims));
    expect(first?.jti).toBeTruthy();
    expect(first?.jti).not.toBe(second?.jti);
  });
});

describe("the guard on each surface", () => {
  const session = (surface: "user" | "admin", mfaSatisfied: boolean) =>
    createJwt({
      sub: "1", email: "a@b.test", role: "SUPER_ADMIN", domainId: "d",
      surface,
      aud: surface === "admin" ? "lambdamail:admin" : "lambdamail:user",
      mfaSatisfied,
      purpose: "session",
    });

  // The webmail guard must agree with the login it issues, or a password-only
  // sign-in would produce a session its own API refuses.
  it("admits a webmail session that never proved a second factor", () => {
    const payload = JSON.parse(
      Buffer.from(session("user", false).split(".")[1], "base64url").toString(),
    );
    expect(isSurfaceAuthorized(payload, "user")).toBe(true);
  });

  it("still refuses the console without one", () => {
    const payload = JSON.parse(
      Buffer.from(session("admin", false).split(".")[1], "base64url").toString(),
    );
    expect(isSurfaceAuthorized(payload, "admin")).toBe(false);
  });
});

import { verifyJwt } from "./session.js";

/**
 * Which surface a login opens, and what crossing between them costs.
 *
 * These rules were spread through the login handler, where two of them were
 * wrong in a way no test could reach:
 *
 *  - Reading your own mail demanded a second factor. The mailbox is already
 *    behind the account's password; it is the console that needs more.
 *  - A session issued at the admin sign-in carried the admin audience alone, so
 *    leaving the console landed on the webmail's login screen - asking for a
 *    password from somebody who had just proven a password *and* a second
 *    factor. Proving more must never grant less.
 */

export type Surface = "user" | "admin";

/**
 * Whether the surface may only open once a second factor has been proven.
 *
 * The console can change any mailbox, any domain and the DNS records the mail
 * flow depends on, which is why PLAN.md section 14.5 makes it unconditional
 * there. The webmail is one account's own mail, so the password that owns it is
 * the bar. An account may still enrol a factor and be asked for it on the way
 * into the console.
 */
export function secondFactorRequired(surface: Surface): boolean {
  return surface === "admin";
}

/**
 * The surfaces a sign-in on `surface` should hand out sessions for.
 *
 * An admin sign-in also yields a webmail session because it satisfied strictly
 * more than the webmail asks for. Issuing both at once is what makes leaving
 * the console a plain link instead of another sign-in.
 */
export function surfacesGrantedBy(surface: Surface): Surface[] {
  return surface === "admin" ? ["admin", "user"] : ["user"];
}

/** Cookies are per surface, so an admin session cannot displace a webmail one. */
export function cookieNameFor(surface: Surface): string {
  return surface === "admin" ? "lm_admin_session" : "lm_user_session";
}

/**
 * Whether this token is a webmail session that may be stepped up to the
 * console by presenting a second factor and nothing else.
 *
 * A challenge or enrolment token is refused: neither proves more than that a
 * password was typed, and accepting one here would turn the step-up into a
 * second way of skipping the factor it exists to collect.
 */
export function stepUpNeedsSecondFactor(token: string | null | undefined): boolean {
  if (!token) return false;
  const payload = verifyJwt(token);
  if (!payload) return false;
  return payload.purpose === "session" && payload.aud === "lambdamail:user";
}

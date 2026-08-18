/**
 * Which session a page needs before it can usefully render.
 *
 * Kept out of the middleware so it can be tested: as two inline prefix checks
 * it could not express the case that matters, that /admin/step-up is reached
 * *with* a webmail session and *without* an admin one. Gating it on an admin
 * session made the screen that mints admin sessions unreachable.
 *
 * This is not the security boundary. Every /api/v1/* call is verified by the
 * service that owns the signing key; this only avoids serving a shell that
 * cannot work, and sends the visitor somewhere useful instead.
 */

export type GatedSurface = "user" | "admin";

/** Paths inside a surface that must stay reachable without its session. */
const OPEN_PATHS = new Set(["/user/login", "/admin/login"]);

/**
 * Paths that live under one surface but are gated on another.
 *
 * The step-up trades a webmail session for an admin one, so requiring the
 * admin session it produces would be circular.
 */
const CROSS_GATED: Record<string, GatedSurface> = {
  "/admin/step-up": "user",
};

/** True when `pathname` is inside `area` as a path segment, not as a prefix. */
function inArea(pathname: string, area: string): boolean {
  return pathname === area || pathname.startsWith(`${area}/`);
}

export function surfaceGateFor(pathname: string): GatedSurface | null {
  if (OPEN_PATHS.has(pathname)) return null;
  if (CROSS_GATED[pathname]) return CROSS_GATED[pathname];
  if (inArea(pathname, "/admin")) return "admin";
  if (inArea(pathname, "/user")) return "user";
  return null;
}

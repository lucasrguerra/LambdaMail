import { NextResponse, type NextRequest } from "next/server";
import { surfaceGateFor } from "./lib/surfaceGate";

/**
 * Gate for the two web surfaces.
 *
 * Without this, /admin/* rendered for anyone who typed the URL: the console
 * was reachable unauthenticated and only its data calls failed. The check here
 * is deliberately shallow - it reads the session cookie and its audience, and
 * does not verify the signature, because the signing key belongs to the auth
 * service and must not be copied into the browser-facing bundle.
 *
 * That is sound because this is not the security boundary: every /api/v1/*
 * request is verified for real by the auth service, which owns the key. This
 * only stops an unauthenticated visitor from loading a shell that cannot work,
 * and sends them somewhere useful instead.
 */

const USER_COOKIE = "lm_user_session";
const ADMIN_COOKIE = "lm_admin_session";

interface JwtClaims {
  aud?: string;
  surface?: string;
  purpose?: string;
  exp?: number;
}

/** Reads the claims without verifying the signature. See the note above. */
function readClaims(token: string | undefined): JwtClaims | null {
  if (!token) return null;
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  try {
    const payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const json = atob(payload.padEnd(payload.length + ((4 - (payload.length % 4)) % 4), "="));
    return JSON.parse(json) as JwtClaims;
  } catch {
    return null;
  }
}

function isUsable(claims: JwtClaims | null, expectedAud: string): boolean {
  if (!claims) return false;
  // A challenge token is issued between the password and the second factor;
  // it must not open either surface.
  if (claims.purpose !== "session") return false;
  if (claims.aud !== expectedAud) return false;
  if (typeof claims.exp === "number" && claims.exp * 1000 <= Date.now()) return false;
  return true;
}

export function middleware(req: NextRequest) {
  const { pathname, search } = req.nextUrl;

  // Which session this path needs is decided by surfaceGateFor, where it is
  // tested. It is not always the surface the path sits under: /admin/step-up
  // exists to turn a webmail session into an admin one, so requiring the admin
  // session would make it unreachable.
  const surface = surfaceGateFor(pathname);
  if (!surface) {
    return NextResponse.next();
  }

  const cookieName = surface === "admin" ? ADMIN_COOKIE : USER_COOKIE;
  const claims = readClaims(req.cookies.get(cookieName)?.value);

  if (isUsable(claims, `lambdamail:${surface}`)) {
    return NextResponse.next();
  }

  // The original destination travels along so the login can return there,
  // rather than always dropping the user on a default page.
  const login = req.nextUrl.clone();
  login.pathname = `/${surface}/login`;
  login.search = "";
  login.searchParams.set("next", `${pathname}${search}`);
  return NextResponse.redirect(login);
}

export const config = {
  matcher: ["/admin/:path*", "/user/:path*"],
};

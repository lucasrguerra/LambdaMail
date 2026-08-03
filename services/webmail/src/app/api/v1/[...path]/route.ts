import { NextRequest, NextResponse } from "next/server";

/**
 * Proxies /api/v1/* to the auth service.
 *
 * The browser cannot reach http://auth:3001 - that name only resolves inside
 * the compose network - so every call the pages make used to 404 against the
 * Next server. Routing them through here keeps the session cookie first-party
 * (same origin, so SameSite=Strict still applies) and keeps the auth service
 * off the public network.
 */

const AUTH_SERVICE_URL = process.env.AUTH_SERVICE_URL ?? "http://auth:3001";
// The mail screens are served by the protocols service, which owns the folder,
// UID, flag and blob logic. Both verify the same session token, so the browser
// sees one origin and one cookie either way.
const PROTOCOLS_SERVICE_URL = process.env.PROTOCOLS_SERVICE_URL ?? "http://protocols:8080";

/**
 * Admin areas the protocols service owns rather than the auth service.
 *
 * Each is here because that process holds something the auth service does not:
 * the DKIM vault, the certificate watcher, and the DNS resolver plus the
 * expected-record spec.
 *
 * Adding a route to protocols without adding it here sends the request to the
 * auth service, which answers 401 from its admin guard - indistinguishable from
 * a session problem, and the reason to keep this list beside the services it
 * names.
 */
const PROTOCOLS_ADMIN_AREAS = new Set(["dkim", "tls", "dns"]);

function upstreamFor(path: string[]): string {
  if (path[0] === "mail") return PROTOCOLS_SERVICE_URL;
  if (path[0] === "admin" && PROTOCOLS_ADMIN_AREAS.has(path[1])) return PROTOCOLS_SERVICE_URL;
  return AUTH_SERVICE_URL;
}

// Hop-by-hop headers must not be forwarded (RFC 9110 section 7.6.1), and the
// upstream sets its own content-length.
const STRIPPED = new Set([
  "host",
  "connection",
  "keep-alive",
  "transfer-encoding",
  "upgrade",
  "proxy-authorization",
  "proxy-authenticate",
  "te",
  "trailer",
  "content-length",
]);

function filterHeaders(source: Headers): Headers {
  const out = new Headers();
  source.forEach((value, key) => {
    if (!STRIPPED.has(key.toLowerCase())) out.append(key, value);
  });
  return out;
}

async function forward(req: NextRequest, path: string[]): Promise<Response> {
  const target = `${upstreamFor(path)}/api/v1/${path.join("/")}${req.nextUrl.search}`;
  const headers = filterHeaders(req.headers);

  // The client's address is what the auth service records on sessions and
  // recovery-code use; without this every login looks like it came from the
  // web container.
  const forwardedFor = req.headers.get("x-forwarded-for");
  if (forwardedFor) headers.set("x-forwarded-for", forwardedFor);

  const hasBody = req.method !== "GET" && req.method !== "HEAD";

  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: req.method,
      headers,
      body: hasBody ? await req.arrayBuffer() : undefined,
      redirect: "manual",
      cache: "no-store",
    });
  } catch {
    // A dead upstream is a gateway problem, not a 404 - the difference decides
    // whether an operator looks at the auth service or at this route.
    return NextResponse.json(
      { error: "UPSTREAM_UNAVAILABLE", message: "Upstream service is unreachable" },
      { status: 502 },
    );
  }

  const responseHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    if (!STRIPPED.has(key.toLowerCase())) responseHeaders.append(key, value);
  });
  // Set-Cookie can legitimately appear more than once (logout clears both
  // surfaces); getSetCookie preserves them where forEach would fold them into
  // one malformed header.
  responseHeaders.delete("set-cookie");
  for (const cookie of upstream.headers.getSetCookie?.() ?? []) {
    responseHeaders.append("set-cookie", cookie);
  }

  return new NextResponse(upstream.body, {
    status: upstream.status,
    headers: responseHeaders,
  });
}

type Context = { params: Promise<{ path: string[] }> };

async function handler(req: NextRequest, ctx: Context): Promise<Response> {
  const { path } = await ctx.params;
  return forward(req, path ?? []);
}

export const GET = handler;
export const POST = handler;
export const PUT = handler;
export const PATCH = handler;
export const DELETE = handler;

// Sessions are per-request; nothing here may be cached or prerendered.
export const dynamic = "force-dynamic";

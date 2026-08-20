import type { NextConfig } from "next";

const PROTOCOLS_SERVICE_URL = process.env.PROTOCOLS_SERVICE_URL ?? "http://protocols:8080";

const nextConfig: NextConfig = {
  output: "standalone",

  // The event stream is a WebSocket upgrade, which a route handler cannot
  // proxy: a handler sees a finished request, not the raw connection. A
  // rewrite hands the socket to Next's own proxy, which does upgrade.
  // Everything else under /api/v1 keeps going through the route handler,
  // which needs to read and rewrite Set-Cookie.
  async rewrites() {
    return [
      {
        source: "/api/v1/events",
        destination: `${PROTOCOLS_SERVICE_URL}/api/v1/events`,
      },
      // The well-known documents belong to the protocols service, but the
      // hostnames senders ask for - mta-sts.<domain> above all - resolve to
      // this app. Without these rewrites the Next 404 page was served in
      // place of the MTA-STS policy: the _mta-sts TXT record advertised a
      // policy that could not be fetched, so every sender fell back to
      // "no-policy-found" and no TLS policy was enforced on inbound mail.
      // Google's daily TLS-RPT report reports precisely that.
      //
      // A rewrite rather than a redirect, because RFC 8461 section 3.3 has
      // the sender fetch that exact HTTPS path and it need not follow one.
      {
        source: "/.well-known/mta-sts.txt",
        destination: `${PROTOCOLS_SERVICE_URL}/.well-known/mta-sts.txt`,
      },
      {
        source: "/.well-known/security.txt",
        destination: `${PROTOCOLS_SERVICE_URL}/.well-known/security.txt`,
      },
      {
        source: "/.well-known/autoconfig/mail/config-v1.1.xml",
        destination: `${PROTOCOLS_SERVICE_URL}/.well-known/autoconfig/mail/config-v1.1.xml`,
      },
    ];
  },
};

export default nextConfig;

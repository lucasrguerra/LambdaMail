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
    ];
  },
};

export default nextConfig;

import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * The well-known documents senders fetch over HTTPS.
 *
 * The protocols service serves /.well-known/mta-sts.txt, but the hostname a
 * sender asks - mta-sts.<domain> - lands on this Next app, which had no route
 * for it and answered its own 404 page. The DNS half of MTA-STS was published
 * and the HTTPS half was not, so every sender saw "no-policy-found" and no
 * TLS policy was ever enforced. Google's daily TLS-RPT report says exactly
 * that.
 */

const config = readFileSync(resolve(process.cwd(), "next.config.ts"), "utf8");

describe("the MTA-STS policy senders fetch", () => {
  it("is routed to the service that serves it", () => {
    expect(config).toContain("/.well-known/mta-sts.txt");
    expect(config).toMatch(/mta-sts\.txt[\s\S]{0,200}PROTOCOLS_SERVICE_URL/);
  });

  it("routes the other well-known documents the same way", () => {
    expect(config).toContain("/.well-known/security.txt");
  });

  // A rewrite, not a redirect: RFC 8461 section 3.3 requires the policy to be
  // fetched over HTTPS from that exact path, and a sender is not obliged to
  // follow a redirect to get it.
  it("rewrites rather than redirects", () => {
    expect(config).not.toMatch(/redirects\(\)/);
  });
});

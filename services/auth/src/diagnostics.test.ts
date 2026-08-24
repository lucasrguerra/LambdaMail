import { describe, it, expect } from "vitest";
import { checkUser, checkServer, overallStatus, type UserFacts, type ServerFacts } from "./diagnostics.js";

const NOW = new Date("2026-06-15T12:00:00Z");

const user = (over: Partial<UserFacts> = {}): UserFacts => ({
  email: "ana@example.test",
  isActive: true,
  lockedUntil: null,
  quotaBytes: 1000,
  usedBytes: 100,
  mfaEnrolled: true,
  aliasCount: 0,
  sieveScriptBytes: null,
  vacationEnabled: false,
  vacationStart: null,
  vacationEnd: null,
  passwordHashPresent: true,
  ...over,
});

const keys = (checks: { key: string }[]) => checks.map((c) => c.key);
const statusOf = (checks: { key: string; status: string }[], key: string) =>
  checks.find((c) => c.key === key)?.status;

describe("user checks", () => {
  it("passes a healthy account", () => {
    const checks = checkUser(user(), NOW);
    expect(overallStatus(checks)).toBe("PASS");
  });

  it("fails a disabled account", () => {
    expect(statusOf(checkUser(user({ isActive: false }), NOW), "userDisabled")).toBe("FAIL");
  });

  // A lockout that has already elapsed no longer blocks anyone; reporting it
  // as a failure sends an operator chasing a problem that resolved itself.
  it("distinguishes a spent lockout from a live one", () => {
    const spent = checkUser(user({ lockedUntil: "2026-06-15T11:00:00Z" }), NOW);
    expect(keys(spent)).toContain("userNotLockedOut");

    const live = checkUser(user({ lockedUntil: "2026-06-15T13:00:00Z" }), NOW);
    expect(statusOf(live, "userLockedOut")).toBe("FAIL");
  });

  // Zero means unlimited. A ratio would divide by zero and mark every
  // unlimited mailbox as full.
  it("does not call an unlimited mailbox full", () => {
    const checks = checkUser(user({ quotaBytes: 0, usedBytes: 5_000_000 }), NOW);
    expect(keys(checks)).toContain("quotaUnlimited");
    expect(keys(checks)).not.toContain("quotaFull");
  });

  it("warns before the quota is reached and fails once it is", () => {
    expect(statusOf(checkUser(user({ quotaBytes: 100, usedBytes: 95 }), NOW), "quotaNearlyFull")).toBe("WARN");
    expect(statusOf(checkUser(user({ quotaBytes: 100, usedBytes: 100 }), NOW), "quotaFull")).toBe("FAIL");
  });

  // Switched on with a window that has closed is the state that reads as
  // broken to whoever set it, so it is called out rather than shown as active.
  it("notices an autoresponder whose window has passed", () => {
    const checks = checkUser(
      user({ vacationEnabled: true, vacationEnd: "2026-01-01T00:00:00Z" }),
      NOW,
    );
    expect(statusOf(checks, "vacationWindowPast")).toBe("WARN");
  });

  it("does not report an autoresponder that is switched off", () => {
    expect(keys(checkUser(user(), NOW)).some((k) => k.startsWith("vacation"))).toBe(false);
  });
});

const server = (over: Partial<ServerFacts> = {}): ServerFacts => ({
  domains: [{ name: "example.test", dnsStatus: "VERIFIED", daneEnabled: false }],
  queueByStatus: {},
  usersOverQuota: 0,
  usersTotal: 3,
  domainsWithoutDkim: [],
  ...over,
});

describe("server checks", () => {
  it("passes a healthy server", () => {
    expect(overallStatus(checkServer(server()))).toBe("PASS");
  });

  it("fails a domain whose DNS is not verified", () => {
    const checks = checkServer(server({ domains: [{ name: "bad.test", dnsStatus: "ERROR", daneEnabled: false }] }));
    expect(statusOf(checks, "dnsNotVerified")).toBe("FAIL");
  });

  // Unsigned mail from a domain that publishes DMARC is treated as a failure
  // by most large receivers, so a missing key is not a warning.
  it("fails a domain with no DKIM key", () => {
    expect(statusOf(checkServer(server({ domainsWithoutDkim: ["example.test"] })), "dkimMissing")).toBe("FAIL");
  });

  it("reports frozen jobs as a failure and deferred ones as a warning", () => {
    const checks = checkServer(server({ queueByStatus: { FROZEN: 2, DEFERRED: 5 } }));
    expect(statusOf(checks, "queueHasFrozen")).toBe("FAIL");
    expect(statusOf(checks, "queueHasDeferred")).toBe("WARN");
  });

  it("says so when there are no domains at all", () => {
    expect(statusOf(checkServer(server({ domains: [] })), "noDomains")).toBe("WARN");
  });
});

describe("overall status", () => {
  it("reports the worst thing present", () => {
    expect(overallStatus([{ key: "a", status: "PASS" }, { key: "b", status: "WARN" }])).toBe("WARN");
    expect(overallStatus([{ key: "a", status: "WARN" }, { key: "b", status: "FAIL" }])).toBe("FAIL");
    expect(overallStatus([{ key: "a", status: "INFO" }])).toBe("INFO");
    expect(overallStatus([])).toBe("INFO");
  });
});

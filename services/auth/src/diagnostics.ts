/**
 * Checks an operator can run by hand, against the whole server or one user.
 *
 * Each check is a pure function over facts already gathered, so the rules can
 * be tested without a database and without sending mail. Gathering the facts
 * is the repository's job; deciding what they mean is here.
 */

export type CheckStatus = "PASS" | "WARN" | "FAIL" | "INFO";

export interface Check {
  /** Stable key: the console translates it, so no English is built here. */
  key: string;
  status: CheckStatus;
  /** What the check looked at - an address, a domain, a count. */
  subject?: string;
  detail?: string;
}

export interface UserFacts {
  email: string;
  isActive: boolean;
  lockedUntil: string | null;
  quotaBytes: number;
  usedBytes: number;
  mfaEnrolled: boolean;
  aliasCount: number;
  sieveScriptBytes: number | null;
  vacationEnabled: boolean;
  vacationStart: string | null;
  vacationEnd: string | null;
  passwordHashPresent: boolean;
}

/** Quota headroom below this is worth saying out loud before mail bounces. */
export const QUOTA_WARN_RATIO = 0.9;

export function checkUser(facts: UserFacts, now: Date = new Date()): Check[] {
  const checks: Check[] = [];

  checks.push({
    key: facts.isActive ? "userActive" : "userDisabled",
    status: facts.isActive ? "PASS" : "FAIL",
    subject: facts.email,
  });

  // A lockout in the past is spent; only a future one still blocks sign-in.
  const locked = facts.lockedUntil !== null && new Date(facts.lockedUntil) > now;
  checks.push({
    key: locked ? "userLockedOut" : "userNotLockedOut",
    status: locked ? "FAIL" : "PASS",
    detail: locked ? String(facts.lockedUntil) : undefined,
  });

  checks.push({
    key: facts.passwordHashPresent ? "userHasPassword" : "userHasNoPassword",
    status: facts.passwordHashPresent ? "PASS" : "FAIL",
  });

  // A zero quota means unlimited here, so a ratio would divide by zero and
  // report every unlimited mailbox as full.
  if (facts.quotaBytes > 0) {
    const ratio = facts.usedBytes / facts.quotaBytes;
    checks.push({
      key: ratio >= 1 ? "quotaFull" : ratio >= QUOTA_WARN_RATIO ? "quotaNearlyFull" : "quotaHealthy",
      status: ratio >= 1 ? "FAIL" : ratio >= QUOTA_WARN_RATIO ? "WARN" : "PASS",
      detail: `${Math.round(ratio * 100)}%`,
    });
  } else {
    checks.push({ key: "quotaUnlimited", status: "INFO" });
  }

  checks.push({
    key: facts.mfaEnrolled ? "mfaEnrolled" : "mfaNotEnrolled",
    status: facts.mfaEnrolled ? "PASS" : "INFO",
  });

  checks.push({
    key: "aliasesDelivering",
    status: "INFO",
    detail: String(facts.aliasCount),
  });

  if (facts.sieveScriptBytes !== null) {
    checks.push({
      key: "sieveScriptPresent",
      status: "INFO",
      detail: `${facts.sieveScriptBytes} B`,
    });
  }

  if (facts.vacationEnabled) {
    // An autoresponder whose window has already closed is switched on and
    // doing nothing, which reads as broken to whoever set it.
    const ended = facts.vacationEnd !== null && new Date(facts.vacationEnd) < now;
    const notYet = facts.vacationStart !== null && new Date(facts.vacationStart) > now;
    checks.push({
      key: ended ? "vacationWindowPast" : notYet ? "vacationWindowFuture" : "vacationActive",
      status: ended ? "WARN" : "INFO",
    });
  }

  return checks;
}

export interface ServerFacts {
  domains: { name: string; dnsStatus: string; daneEnabled: boolean }[];
  queueByStatus: Record<string, number>;
  usersOverQuota: number;
  usersTotal: number;
  domainsWithoutDkim: string[];
}

export function checkServer(facts: ServerFacts): Check[] {
  const checks: Check[] = [];

  for (const d of facts.domains) {
    checks.push({
      key: d.dnsStatus === "VERIFIED" ? "dnsVerified" : "dnsNotVerified",
      status: d.dnsStatus === "VERIFIED" ? "PASS" : d.dnsStatus === "PENDING" ? "WARN" : "FAIL",
      subject: d.name,
      detail: d.dnsStatus,
    });
  }
  if (facts.domains.length === 0) {
    checks.push({ key: "noDomains", status: "WARN" });
  }

  for (const name of facts.domainsWithoutDkim) {
    // Without a DKIM key the domain signs nothing, and most large receivers
    // treat unsigned mail from a domain that publishes DMARC as a failure.
    checks.push({ key: "dkimMissing", status: "FAIL", subject: name });
  }

  const bounced = facts.queueByStatus.BOUNCED ?? 0;
  const deferred = facts.queueByStatus.DEFERRED ?? 0;
  const frozen = facts.queueByStatus.FROZEN ?? 0;

  checks.push({
    key: bounced > 0 ? "queueHasBounces" : "queueNoBounces",
    status: bounced > 0 ? "WARN" : "PASS",
    detail: String(bounced),
  });
  checks.push({
    key: deferred > 0 ? "queueHasDeferred" : "queueNoDeferred",
    status: deferred > 0 ? "WARN" : "PASS",
    detail: String(deferred),
  });
  if (frozen > 0) {
    checks.push({ key: "queueHasFrozen", status: "FAIL", detail: String(frozen) });
  }

  checks.push({
    key: facts.usersOverQuota > 0 ? "usersOverQuota" : "noUsersOverQuota",
    status: facts.usersOverQuota > 0 ? "WARN" : "PASS",
    detail: `${facts.usersOverQuota}/${facts.usersTotal}`,
  });

  return checks;
}

/** The worst status present, which is what a summary badge should show. */
export function overallStatus(checks: Check[]): CheckStatus {
  if (checks.some((c) => c.status === "FAIL")) return "FAIL";
  if (checks.some((c) => c.status === "WARN")) return "WARN";
  if (checks.some((c) => c.status === "PASS")) return "PASS";
  return "INFO";
}

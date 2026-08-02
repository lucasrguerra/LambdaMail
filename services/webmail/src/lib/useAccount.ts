"use client";

import { useEffect, useState } from "react";

export interface Account {
  id: string;
  email: string;
  role: "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN";
  locale: string;
  mfa_enrolled: boolean;
  recovery_codes_left: number;
}

/** Roles that may open the admin console. */
export function isAdminRole(role: string | undefined): boolean {
  return role === "SUPER_ADMIN" || role === "DOMAIN_ADMIN";
}

/**
 * The signed-in account for the surface this component is rendered on.
 *
 * Both sidebars used to print a fixed "admin@lambdamail.local" / "SUPER_ADMIN
 * (2FA)" - placeholder text that survived into production and told every
 * operator the wrong address and, worse, claimed a second factor was active
 * whether or not it was.
 *
 * The endpoint differs per surface because the auth service selects the session
 * cookie from the URL prefix; see the note on its `me` handler.
 */
export function useAccount(surface: "user" | "admin"): Account | null {
  const [account, setAccount] = useState<Account | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch(`/api/v1/${surface}/me`)
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (!cancelled) setAccount(data);
      })
      // A failure leaves the caller with null, which renders as a placeholder
      // rather than as somebody else's address.
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [surface]);

  return account;
}

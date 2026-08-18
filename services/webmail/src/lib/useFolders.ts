"use client";

import { useCallback, useEffect, useState } from "react";
import { useMailEvents } from "./useMailEvents";
import type { FolderSummary } from "./mailCounts";

/**
 * The folder list, kept current.
 *
 * It used to be fetched once when the sidebar mounted, which froze every unread
 * badge at whatever it was when the tab was opened: mail arriving did not raise
 * it, and reading a message did not lower it. Two things move these counters,
 * so both have to reach here - the server's push for anything that happened
 * elsewhere, and an in-page announcement for what this tab just did.
 */

/**
 * Fired on window when this tab changes something a counter is derived from.
 *
 * A DOM event rather than shared React state because the two are in different
 * subtrees: the reader is inside the page, the badges are in the layout around
 * it, and neither renders the other.
 */
export const MAIL_STATE_CHANGED = "lm:mail-state-changed";

/** Announces that this tab moved a counter, so the badges re-read them. */
export function notifyMailStateChanged(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(MAIL_STATE_CHANGED));
}

export function useFolders(): { folders: FolderSummary[]; reload: () => void } {
  const [folders, setFolders] = useState<FolderSummary[]>([]);

  const reload = useCallback(() => {
    void fetch("/api/v1/mail/folders", { cache: "no-store" })
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        // Only a real list replaces the current one: overwriting with [] on a
        // failed request would blank every badge on a transient error.
        if (Array.isArray(data)) setFolders(data as FolderSummary[]);
      })
      .catch(() => undefined);
  }, []);

  useEffect(reload, [reload]);

  // Any push means something changed in the mailbox; which folder it landed in
  // is not worth deducing here when re-reading the counters is one small call.
  useMailEvents(useCallback(() => reload(), [reload]));

  useEffect(() => {
    window.addEventListener(MAIL_STATE_CHANGED, reload);
    return () => window.removeEventListener(MAIL_STATE_CHANGED, reload);
  }, [reload]);

  return { folders, reload };
}

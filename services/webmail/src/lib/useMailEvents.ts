"use client";

import { useEffect, useRef } from "react";

interface MailEvent {
  type: string;
  mailbox_id: string;
  payload: Record<string, unknown>;
}

// Reconnection backs off so a server restart does not turn every open tab into
// a reconnect storm, and is capped so a tab left open still recovers promptly.
const INITIAL_RETRY_MS = 1000;
const MAX_RETRY_MS = 30000;

/**
 * Subscribes to the server's event stream and calls onEvent for each push.
 *
 * The callback is also invoked once on every successful (re)connect, with a
 * synthetic "resync" event: while the socket was down the mailbox may have
 * changed, and reconnecting without refetching would leave the list quietly
 * stale - which is worse than not having real-time updates at all.
 */
export function useMailEvents(onEvent: (event: MailEvent) => void): void {
  // Held in a ref so a re-render with a new callback identity does not tear
  // the socket down and reconnect.
  const handler = useRef(onEvent);
  handler.current = onEvent;

  useEffect(() => {
    let socket: WebSocket | null = null;
    let retry = INITIAL_RETRY_MS;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let closed = false;

    const connect = () => {
      if (closed) return;

      const scheme = window.location.protocol === "https:" ? "wss" : "ws";
      socket = new WebSocket(`${scheme}://${window.location.host}/api/v1/events`);

      socket.onopen = () => {
        retry = INITIAL_RETRY_MS;
        handler.current({ type: "resync", mailbox_id: "", payload: {} });
      };

      socket.onmessage = (message) => {
        try {
          handler.current(JSON.parse(message.data) as MailEvent);
        } catch {
          // A malformed frame is not worth tearing the connection down for.
        }
      };

      socket.onclose = () => {
        if (closed) return;
        retryTimer = setTimeout(connect, retry);
        retry = Math.min(retry * 2, MAX_RETRY_MS);
      };

      // onerror is followed by onclose, which already schedules the retry.
      socket.onerror = () => socket?.close();
    };

    connect();

    return () => {
      closed = true;
      if (retryTimer) clearTimeout(retryTimer);
      socket?.close();
    };
  }, []);
}

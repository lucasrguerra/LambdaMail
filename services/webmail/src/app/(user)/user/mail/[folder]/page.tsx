"use client";

import React, { use, useCallback, useEffect, useState } from "react";
import { useTranslations } from "../../../../../i18n/provider";
import { useMailEvents } from "../../../../../lib/useMailEvents";

interface MessageSummary {
  uid: number;
  subject: string;
  sender_address: string;
  from_display_name: string;
  snippet: string;
  size_bytes: number;
  received_at: string;
  has_attachments: boolean;
  seen: boolean;
  spam_verdict: string;
  dmarc_result: string;
}

interface RenderedMessage {
  uid: number;
  subject: string;
  from: string;
  to: string[];
  cc: string[];
  date: string;
  text: string;
  html: string;
  attachments: string[];
}

/** Strips remote references so a tracking pixel cannot phone home before the
 * user asks for images. The message still renders; only external loads go. */
function blockRemoteContent(html: string): string {
  return html
    .replace(/(<img[^>]+)src\s*=\s*("|')https?:[^"']*\2/gi, "$1data-blocked-src=$2$2")
    .replace(/(<[^>]+)background\s*=\s*("|')https?:[^"']*\2/gi, "$1")
    .replace(/url\(\s*['"]?https?:[^)]*\)/gi, "none");
}

export default function MailFolderPage({ params }: { params: Promise<{ folder: string }> }) {
  const { folder } = use(params);
  const t = useTranslations();

  const [messages, setMessages] = useState<MessageSummary[]>([]);
  const [selected, setSelected] = useState<RenderedMessage | null>(null);
  const [loadRemoteImages, setLoadRemoteImages] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadMessages = useCallback(
    async (search: string) => {
      setLoading(true);
      setError(null);
      try {
        const query = new URLSearchParams({ folder, ...(search ? { q: search } : {}) });
        const res = await fetch(`/api/v1/mail/messages?${query}`);
        if (res.status === 401) {
          window.location.href = "/user/login";
          return;
        }
        if (!res.ok) throw new Error(t("errors.serverError"));
        setMessages(await res.json());
      } catch (err) {
        setError(err instanceof Error ? err.message : t("errors.serverError"));
      } finally {
        setLoading(false);
      }
    },
    [folder, t],
  );

  useEffect(() => {
    void loadMessages("");
  }, [loadMessages]);

  // Push updates, plus a refetch on every (re)connect: while the socket was
  // down the folder may have changed, and reconnecting without reconciling
  // would leave the list quietly stale.
  useMailEvents(
    useCallback(
      (event) => {
        if (event.type === "resync" || event.type === "EmailReceived") {
          void loadMessages(searchQuery);
        }
      },
      [loadMessages, searchQuery],
    ),
  );

  // Debounced so typing in the search box does not issue a query per keystroke.
  useEffect(() => {
    const handle = setTimeout(() => void loadMessages(searchQuery), 250);
    return () => clearTimeout(handle);
  }, [searchQuery, loadMessages]);

  const openMessage = async (uid: number) => {
    setLoadRemoteImages(false);
    try {
      const res = await fetch(`/api/v1/mail/message/${uid}?folder=${encodeURIComponent(folder)}`);
      if (!res.ok) throw new Error(t("errors.serverError"));
      setSelected(await res.json());
      // Reading marks it seen server-side; reflect that without a refetch.
      setMessages((current) => current.map((m) => (m.uid === uid ? { ...m, seen: true } : m)));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("errors.serverError"));
    }
  };

  const bodyHtml = selected?.html
    ? loadRemoteImages
      ? selected.html
      : blockRemoteContent(selected.html)
    : null;

  return (
    <div className="flex-1 flex overflow-hidden">
      <div className="w-96 border-r border-slate-800 flex flex-col bg-slate-900/30">
        <div className="p-3 border-b border-slate-800">
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder={t("common.search")}
            className="w-full px-3 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
          />
        </div>

        <div className="flex-1 overflow-y-auto divide-y divide-slate-800/60">
          {loading ? (
            <div className="p-4 text-xs text-slate-500">{t("common.loading")}</div>
          ) : error ? (
            <div className="p-4 text-xs text-red-400">{error}</div>
          ) : messages.length === 0 ? (
            <div className="p-4 text-xs text-slate-500 italic">{t("mail.noMessages")}</div>
          ) : (
            messages.map((msg) => (
              <button
                key={msg.uid}
                onClick={() => void openMessage(msg.uid)}
                className={`w-full text-left p-4 transition-colors block ${
                  selected?.uid === msg.uid
                    ? "bg-indigo-600/10 border-l-2 border-indigo-500"
                    : "hover:bg-slate-800/40"
                }`}
              >
                <div className="flex items-center justify-between mb-1">
                  <span className={`text-xs font-semibold ${msg.seen ? "text-slate-300" : "text-white"}`}>
                    {msg.from_display_name || msg.sender_address}
                  </span>
                  <span className="text-[10px] text-slate-500">
                    {new Date(msg.received_at).toLocaleString()}
                  </span>
                </div>
                <div className={`text-xs truncate mb-1 ${msg.seen ? "text-slate-300" : "font-bold text-slate-100"}`}>
                  {msg.subject || "(no subject)"}
                  {msg.has_attachments ? " \u{1F4CE}" : ""}
                </div>
                <div className="text-[11px] text-slate-400 truncate">{msg.snippet}</div>
              </button>
            ))
          )}
        </div>
      </div>

      <div className="flex-1 flex flex-col bg-slate-950 overflow-y-auto p-6">
        {selected ? (
          <div className="max-w-3xl w-full mx-auto">
            <div className="border-b border-slate-800 pb-6 mb-6">
              <h1 className="text-xl font-bold text-white mb-4">{selected.subject || "(no subject)"}</h1>
              <div className="flex items-center justify-between text-xs text-slate-400">
                <div className="font-semibold text-slate-200">{selected.from}</div>
                <div>{selected.date ? new Date(selected.date).toLocaleString() : ""}</div>
              </div>
              {selected.to.length > 0 && (
                <div className="mt-1 text-[11px] text-slate-500">To: {selected.to.join(", ")}</div>
              )}
              {selected.attachments.length > 0 && (
                <div className="mt-2 text-[11px] text-slate-400">
                  {"\u{1F4CE} "}
                  {selected.attachments.join(", ")}
                </div>
              )}
            </div>

            {selected.html && (
              <div className="mb-4 p-3 rounded-lg bg-amber-500/10 border border-amber-500/20 flex items-center justify-between text-xs text-amber-300">
                <span>{t("mail.loadImages")}</span>
                <button
                  onClick={() => setLoadRemoteImages(!loadRemoteImages)}
                  className="px-3 py-1 rounded bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 font-medium transition-colors"
                >
                  {loadRemoteImages ? t("common.confirm") : t("mail.loadImages")}
                </button>
              </div>
            )}

            {bodyHtml ? (
              <div className="bg-white rounded-xl overflow-hidden shadow-xl border border-slate-800 min-h-[300px]">
                {/* Sandboxed with no allow-scripts and no same-origin: hostile
                    markup in a message cannot run or reach this session. */}
                <iframe
                  title="Message body"
                  srcDoc={bodyHtml}
                  sandbox=""
                  className="w-full h-96 border-0"
                />
              </div>
            ) : (
              <pre className="whitespace-pre-wrap break-words text-sm text-slate-200 font-sans">
                {selected.text}
              </pre>
            )}
          </div>
        ) : (
          <div className="flex-1 flex items-center justify-center text-slate-500 text-sm">
            {t("mail.noMessages")}
          </div>
        )}
      </div>
    </div>
  );
}

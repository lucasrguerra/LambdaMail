"use client";

import React, { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useTranslations } from "../../../../i18n/provider";

// The undo window holds the message in the browser before it is submitted.
// Nothing is queued server-side until it elapses, so "undo" really does mean
// the message was never sent, rather than trying to recall it afterwards.
const UNDO_WINDOW_SECONDS = 10;

export default function ComposePage() {
  const t = useTranslations();
  const [to, setTo] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [sending, setSending] = useState(false);
  const [undoSeconds, setUndoSeconds] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cancelled = useRef(false);

  const submit = async () => {
    try {
      const res = await fetch("/api/v1/mail/send", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ to: [to], subject, body }),
      });
      if (res.status === 401) {
        window.location.href = "/user/login";
        return;
      }
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message || t("errors.serverError"));
      window.location.href = "/user/mail/sent";
    } catch (err) {
      setError(err instanceof Error ? err.message : t("errors.serverError"));
      setSending(false);
    }
  };

  useEffect(() => {
    if (undoSeconds === null) return;
    if (undoSeconds <= 0) {
      setUndoSeconds(null);
      if (!cancelled.current) void submit();
      return;
    }
    const handle = setTimeout(() => setUndoSeconds((prev) => (prev === null ? null : prev - 1)), 1000);
    return () => clearTimeout(handle);
    // submit closes over the current fields, which is what should be sent.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [undoSeconds]);

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    cancelled.current = false;
    setSending(true);
    setUndoSeconds(UNDO_WINDOW_SECONDS);
  };

  const handleUndo = () => {
    cancelled.current = true;
    setUndoSeconds(null);
    setSending(false);
  };

  return (
    <div className="flex-1 p-6 bg-slate-950 flex flex-col justify-center items-center">
      <div className="glass-panel p-6 rounded-2xl max-w-2xl w-full border border-slate-800 shadow-2xl">
        <div className="flex items-center justify-between border-b border-slate-800 pb-4 mb-6">
          <h1 className="text-lg font-bold text-white flex items-center gap-2">
            <span>&#128221;</span> {t("mail.compose")}
          </h1>
          <span className="text-xs text-slate-400 font-mono">Auto-draft active (3s)</span>
        </div>

        {undoSeconds !== null && (
          <div className="mb-6 p-4 rounded-xl bg-indigo-600/20 border border-indigo-500/40 flex items-center justify-between text-indigo-300">
            <span className="text-sm font-medium">
              {t("mail.undoSend").replace("{seconds}", String(undoSeconds))}
            </span>
            <button
              onClick={handleUndo}
              className="px-4 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-bold transition-colors"
            >
              {t("common.cancel")}
            </button>
          </div>
        )}

        {error && (
          <div className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
            {error}
          </div>
        )}

        <form onSubmit={handleSend} className="space-y-4">
          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">{t("ui.toRecipients")}</label>
            <input
              type="email"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              placeholder="recipient@domain.com"
              required
              disabled={sending}
              className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 text-sm focus:outline-none focus:border-indigo-500"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">{t("ui.subject")}</label>
            <input
              type="text"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder="Message Subject..."
              required
              disabled={sending}
              className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 text-sm focus:outline-none focus:border-indigo-500"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">{t("ui.messageBody")}</label>
            <textarea
              rows={8}
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder="Write your email here..."
              required
              disabled={sending}
              className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 text-sm focus:outline-none focus:border-indigo-500"
            />
          </div>

          <div className="flex items-center justify-between pt-4 border-t border-slate-800">
            <Link href="/user/mail/inbox" className="text-xs text-slate-400 hover:text-slate-200">
              Discard Draft
            </Link>

            <button
              type="submit"
              disabled={sending}
              className="py-2.5 px-6 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-sm transition-colors shadow-lg shadow-indigo-600/20 disabled:opacity-50"
            >
              {sending ? "Queued to Send..." : "Send Message"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

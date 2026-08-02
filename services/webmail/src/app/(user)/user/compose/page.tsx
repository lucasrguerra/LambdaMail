"use client";

import React, { useState } from "react";
import Link from "next/link";

export default function ComposePage() {
  const [to, setTo] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [sending, setSending] = useState(false);
  const [undoSeconds, setUndoSeconds] = useState<number | null>(null);

  const handleSend = (e: React.FormEvent) => {
    e.preventDefault();
    setSending(true);
    setUndoSeconds(30);

    const interval = setInterval(() => {
      setUndoSeconds((prev) => {
        if (prev === null || prev <= 1) {
          clearInterval(interval);
          setSending(false);
          window.location.href = "/user/mail/sent";
          return null;
        }
        return prev - 1;
      });
    }, 1000);
  };

  const handleUndo = () => {
    setUndoSeconds(null);
    setSending(false);
  };

  return (
    <div className="flex-1 p-6 bg-slate-950 flex flex-col justify-center items-center">
      <div className="glass-panel p-6 rounded-2xl max-w-2xl w-full border border-slate-800 shadow-2xl">
        <div className="flex items-center justify-between border-b border-slate-800 pb-4 mb-6">
          <h1 className="text-lg font-bold text-white flex items-center gap-2">
            <span>&#128221;</span> Compose Email Message
          </h1>
          <span className="text-xs text-slate-400 font-mono">Auto-draft active (3s)</span>
        </div>

        {undoSeconds !== null && (
          <div className="mb-6 p-4 rounded-xl bg-indigo-600/20 border border-indigo-500/40 flex items-center justify-between text-indigo-300">
            <span className="text-sm font-medium">
              Email queued! Sending in <strong>{undoSeconds}s</strong>...
            </span>
            <button
              onClick={handleUndo}
              className="px-4 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-bold transition-colors"
            >
              Undo Send
            </button>
          </div>
        )}

        <form onSubmit={handleSend} className="space-y-4">
          <div>
            <label className="block text-xs font-medium text-slate-300 mb-1">To (Recipients)</label>
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
            <label className="block text-xs font-medium text-slate-300 mb-1">Subject</label>
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
            <label className="block text-xs font-medium text-slate-300 mb-1">Message Body</label>
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

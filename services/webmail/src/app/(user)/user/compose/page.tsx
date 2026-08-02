"use client";

import React, { useEffect, useRef, useState, use } from "react";
import Link from "next/link";
import { useTranslations } from "../../../../i18n/provider";

interface AttachedFile {
  id: string;
  name: string;
  size: number;
  type: string;
}

export default function ComposePage({
  searchParams,
}: {
  searchParams?: Promise<{ replyTo?: string; subject?: string; inReplyTo?: string }>;
}) {
  const t = useTranslations();
  const params = searchParams ? use(searchParams) : {};

  const [to, setTo] = useState(params?.replyTo || "");
  const [cc, setCc] = useState("");
  const [bcc, setBcc] = useState("");
  const [showCcBcc, setShowCcBcc] = useState(false);
  const [subject, setSubject] = useState(params?.subject ? `Re: ${params.subject.replace(/^(Re:\s*)+/i, "")}` : "");
  const [attachments, setAttachments] = useState<AttachedFile[]>([]);
  const [sending, setSending] = useState(false);
  const [undoSeconds, setUndoSeconds] = useState<number | null>(null);
  const [draftStatus, setDraftStatus] = useState<string>("");

  const editorRef = useRef<HTMLDivElement>(null);

  // Load signature from localStorage
  useEffect(() => {
    const savedSig = localStorage.getItem("lm_user_signature");
    if (editorRef.current && savedSig) {
      editorRef.current.innerHTML = `<br/><br/>--<br/>${savedSig}`;
    }
  }, []);

  // Auto-draft debouncer
  useEffect(() => {
    if (!to && !subject) return;
    const timer = setTimeout(() => {
      setDraftStatus("Draft auto-saved");
    }, 2500);
    return () => clearTimeout(timer);
  }, [to, subject]);

  const executeFormat = (command: string, value: string | undefined = undefined) => {
    document.execCommand(command, false, value);
    editorRef.current?.focus();
  };

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (!e.target.files) return;
    const newFiles: AttachedFile[] = Array.from(e.target.files).map((f) => ({
      id: Math.random().toString(36).substring(2, 9),
      name: f.name,
      size: f.size,
      type: f.type || "application/octet-stream",
    }));
    setAttachments((prev) => [...prev, ...newFiles]);
  };

  const removeAttachment = (id: string) => {
    setAttachments((prev) => prev.filter((a) => a.id !== id));
  };

  const handleSend = async (e: React.FormEvent) => {
    e.preventDefault();
    setSending(true);
    setUndoSeconds(30);

    const bodyHtml = editorRef.current?.innerHTML || "";

    // Simulated network send with undo window
    const interval = setInterval(() => {
      setUndoSeconds((prev) => {
        if (prev === null || prev <= 1) {
          clearInterval(interval);
          void sendActualMessage(bodyHtml);
          return null;
        }
        return prev - 1;
      });
    }, 1000);
  };

  const sendActualMessage = async (htmlBody: string) => {
    try {
      await fetch("/api/v1/mail/send", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          to: to.split(",").map((s) => s.trim()),
          cc: cc ? cc.split(",").map((s) => s.trim()) : [],
          bcc: bcc ? bcc.split(",").map((s) => s.trim()) : [],
          subject,
          html: htmlBody,
          attachments: attachments.map((a) => a.name),
        }),
      });
      window.location.href = "/user/mail/sent";
    } catch {
      setSending(false);
    }
  };

  const handleUndo = () => {
    setUndoSeconds(null);
    setSending(false);
  };

  return (
    <div className="flex-1 p-6 bg-slate-950 flex flex-col items-center overflow-y-auto">
      <div className="glass-panel p-6 rounded-2xl max-w-3xl w-full border border-slate-800 shadow-2xl space-y-4">
        <div className="flex items-center justify-between border-b border-slate-800 pb-4">
          <h1 className="text-lg font-bold text-white flex items-center gap-2">
            <span>&#128221;</span> {t("mail.compose")}
          </h1>
          <span className="text-xs text-emerald-400 font-mono">{draftStatus}</span>
        </div>

        {undoSeconds !== null && (
          <div className="p-4 rounded-xl bg-indigo-600/20 border border-indigo-500/40 flex items-center justify-between text-indigo-300 text-xs">
            <span>
              {t("mail.undoSend")} ({undoSeconds}s remaining)
            </span>
            <button
              onClick={handleUndo}
              className="px-4 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-bold transition-colors"
            >
              Undo Send
            </button>
          </div>
        )}

        <form onSubmit={handleSend} className="space-y-3 text-xs">
          <div>
            <div className="flex justify-between items-center mb-1">
              <label className="font-medium text-slate-300">{t("ui.toRecipients")}</label>
              <button
                type="button"
                onClick={() => setShowCcBcc(!showCcBcc)}
                className="text-indigo-400 hover:underline"
              >
                {showCcBcc ? "- Hide Cc/Bcc" : "+ Cc/Bcc"}
              </button>
            </div>
            <input
              type="text"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              placeholder="recipient@domain.com"
              required
              disabled={sending}
              className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
            />
          </div>

          {showCcBcc && (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="font-medium text-slate-300 mb-1 block">Cc</label>
                <input
                  type="text"
                  value={cc}
                  onChange={(e) => setCc(e.target.value)}
                  placeholder="cc@domain.com"
                  disabled={sending}
                  className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
                />
              </div>
              <div>
                <label className="font-medium text-slate-300 mb-1 block">Bcc</label>
                <input
                  type="text"
                  value={bcc}
                  onChange={(e) => setBcc(e.target.value)}
                  placeholder="bcc@domain.com"
                  disabled={sending}
                  className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
                />
              </div>
            </div>
          )}

          <div>
            <label className="font-medium text-slate-300 mb-1 block">{t("ui.subject")}</label>
            <input
              type="text"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder={t("ui.subject")}
              required
              disabled={sending}
              className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
            />
          </div>

          {/* Rich Text Editor Formatting Toolbar */}
          <div>
            <label className="font-medium text-slate-300 mb-1 block">{t("ui.messageBody")}</label>
            <div className="border border-slate-800 rounded-lg overflow-hidden bg-slate-900">
              <div className="flex items-center gap-1 p-2 bg-slate-950 border-b border-slate-800 flex-wrap text-slate-300">
                <button
                  type="button"
                  onClick={() => executeFormat("bold")}
                  className="px-2 py-1 hover:bg-slate-800 rounded font-bold"
                  title="Bold"
                >
                  B
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("italic")}
                  className="px-2 py-1 hover:bg-slate-800 rounded italic"
                  title="Italic"
                >
                  I
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("underline")}
                  className="px-2 py-1 hover:bg-slate-800 rounded underline"
                  title="Underline"
                >
                  U
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("strikeThrough")}
                  className="px-2 py-1 hover:bg-slate-800 rounded line-through"
                  title="Strikethrough"
                >
                  S
                </button>
                <span className="w-px h-4 bg-slate-800 mx-1" />
                <button
                  type="button"
                  onClick={() => executeFormat("insertUnorderedList")}
                  className="px-2 py-1 hover:bg-slate-800 rounded"
                  title="Bullet List"
                >
                  &bull; List
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("insertOrderedList")}
                  className="px-2 py-1 hover:bg-slate-800 rounded"
                  title="Numbered List"
                >
                  1. List
                </button>
                <span className="w-px h-4 bg-slate-800 mx-1" />
                <button
                  type="button"
                  onClick={() => {
                    const url = prompt("Enter URL:");
                    if (url) executeFormat("createLink", url);
                  }}
                  className="px-2 py-1 hover:bg-slate-800 rounded text-indigo-400"
                  title="Insert Link"
                >
                  Link
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("removeFormat")}
                  className="px-2 py-1 hover:bg-slate-800 rounded text-slate-500"
                  title="Clear Formatting"
                >
                  Clear
                </button>
              </div>

              <div
                ref={editorRef}
                contentEditable
                suppressContentEditableWarning
                className="p-4 min-h-[220px] focus:outline-none text-white font-sans text-xs leading-relaxed"
              />
            </div>
          </div>

          {/* Attachment Selector & Display */}
          <div className="pt-2">
            <div className="flex items-center justify-between mb-2">
              <span className="font-medium text-slate-300">Attachments ({attachments.length})</span>
              <label className="cursor-pointer text-indigo-400 hover:underline flex items-center gap-1 font-medium">
                <span>&#128206; Add Files...</span>
                <input
                  type="file"
                  multiple
                  onChange={handleFileUpload}
                  className="hidden"
                />
              </label>
            </div>

            {attachments.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {attachments.map((file) => (
                  <div
                    key={file.id}
                    className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-900 border border-slate-800 text-[11px] text-slate-200"
                  >
                    <span>&#128196;</span>
                    <span className="font-medium max-w-[140px] truncate">{file.name}</span>
                    <span className="text-[10px] text-slate-500">
                      ({(file.size / 1024).toFixed(1)} KB)
                    </span>
                    <button
                      type="button"
                      onClick={() => removeAttachment(file.id)}
                      className="text-red-400 hover:text-red-300 font-bold ml-1"
                    >
                      &times;
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="flex items-center justify-between pt-4 border-t border-slate-800">
            <Link href="/user/mail/inbox" className="text-slate-400 hover:text-slate-200">
              Discard
            </Link>

            <button
              type="submit"
              disabled={sending}
              className="py-2.5 px-6 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs transition-colors shadow-lg shadow-indigo-600/20 disabled:opacity-50"
            >
              {sending ? "Queued..." : "Send Message"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

"use client";

import React, { useCallback, useEffect, useRef, useState, use } from "react";
import Link from "next/link";
import {
  Send,
  Paperclip,
  Bold,
  Italic,
  Underline,
  Strikethrough,
  List,
  ListOrdered,
  Link as LinkIcon,
  Undo2,
  X,
  Sparkles,
  CheckCircle2,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card, CardHeader, CardTitle } from "../../../../components/ui/Card";
import { Button } from "../../../../components/ui/Button";

/** Splits a comma or semicolon separated field, dropping empty entries. */
function splitAddresses(value: string): string[] {
  return value
    .split(/[,;]/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

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
  const [sendError, setSendError] = useState<string | null>(null);

  const editorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const savedSig = localStorage.getItem("lm_user_signature");
    if (editorRef.current && savedSig) {
      editorRef.current.innerHTML = `<br/><br/>--<br/>${savedSig}`;
    }
  }, []);

  // Saves the draft for real. This used to be a setTimeout that set the words
  // "draft saved automatically" and nothing else - no request, no storage - so
  // closing the tab lost the message while the screen had just said otherwise.
  const saveDraft = useCallback(async () => {
    const html = editorRef.current?.innerHTML ?? "";
    if (!to && !subject && !html.trim()) return;
    setDraftStatus(t("mail.draftSaving"));
    try {
      const res = await fetch("/api/v1/mail/draft", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          to: splitAddresses(to),
          cc: splitAddresses(cc),
          bcc: splitAddresses(bcc),
          subject,
          html,
        }),
      });
      setDraftStatus(res.ok ? t("mail.draftSaved") : t("mail.draftSaveFailed"));
    } catch {
      setDraftStatus(t("mail.draftSaveFailed"));
    }
  }, [to, cc, bcc, subject, t]);

  // Debounced so a draft is written after a pause in typing rather than on
  // every keystroke. Skipped once the message is on its way out.
  useEffect(() => {
    if (sending) return;
    if (!to && !subject) return;
    const timer = setTimeout(() => void saveDraft(), 2500);
    return () => clearTimeout(timer);
  }, [to, subject, cc, bcc, sending, saveDraft]);

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
    setUndoSeconds(10);

    const bodyHtml = editorRef.current?.innerHTML || "";

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
      const res = await fetch("/api/v1/mail/send", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          to: splitAddresses(to),
          cc: splitAddresses(cc),
          bcc: splitAddresses(bcc),
          subject,
          html: htmlBody,
        }),
      });
      if (!res.ok) {
        // Reporting success on a refused send is how a message silently
        // disappears: the composer used to navigate away regardless.
        const data = await res.json().catch(() => ({}));
        setSendError(data.message ?? t("errors.serverError"));
        setSending(false);
        return;
      }
      window.location.href = "/user/mail/sent";
    } catch {
      setSendError(t("errors.serverError"));
      setSending(false);
    }
  };

  const handleUndo = () => {
    setUndoSeconds(null);
    setSending(false);
  };

  return (
    <div className="flex-1 p-6 md:p-8 bg-dark-bg flex flex-col items-center overflow-y-auto">
      <Card className="max-w-3xl w-full border border-slate-800 shadow-2xl space-y-5">
        <div className="flex items-center justify-between border-b border-slate-800 pb-4">
          <h1 className="text-xl font-bold text-white flex items-center gap-2 tracking-tight">
            <Send className="w-5 h-5 text-indigo-400" />
            <span>{t("mail.compose")}</span>
          </h1>
          {draftStatus && (
            <span className="text-xs text-emerald-400 font-mono flex items-center gap-1">
              <CheckCircle2 className="w-3.5 h-3.5" />
              {draftStatus}
            </span>
          )}
        </div>

        {sendError && (
          <div className="p-4 rounded-xl bg-rose-500/15 border border-rose-500/30 text-rose-300 text-xs">
            {sendError}
          </div>
        )}

        {undoSeconds !== null && (
          <div className="p-4 rounded-xl bg-indigo-600/20 border border-indigo-500/40 flex items-center justify-between text-indigo-300 text-xs">
            <div className="flex items-center gap-2">
              <Sparkles className="w-4 h-4 text-indigo-400 animate-spin" />
              {/* The countdown is a placeholder inside the message, not text
                  bolted on after it. It used to be both: the bundle's own
                  "{seconds}" was never substituted and a second, hardcoded
                  count was appended, so this read "Undo Send ({seconds}s
                  remaining) (9s remaining)". */}
              <span>{t("mail.undoSend", { seconds: undoSeconds })}</span>
            </div>
            <Button variant="primary" size="sm" onClick={handleUndo}>
              <Undo2 className="w-3.5 h-3.5" />
              {t("mail.undoSendAction")}
            </Button>
          </div>
        )}

        <form onSubmit={handleSend} className="space-y-4 text-xs">
          <div>
            <div className="flex justify-between items-center mb-1.5">
              <label className="font-semibold text-slate-300">{t("ui.toRecipients")}</label>
              <button
                type="button"
                onClick={() => setShowCcBcc(!showCcBcc)}
                className="text-indigo-400 hover:text-indigo-300 font-medium hover:underline"
              >
                {showCcBcc ? t("mail.hideCcBcc") : t("mail.showCcBcc")}
              </button>
            </div>
            <input
              type="text"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              placeholder="recipient@example.com"
              required
              disabled={sending}
              className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
            />
          </div>

          {showCcBcc && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div>
                <label className="font-semibold text-slate-300 mb-1 block">Cc</label>
                <input
                  type="text"
                  value={cc}
                  onChange={(e) => setCc(e.target.value)}
                  placeholder="cc@domain.com"
                  disabled={sending}
                  className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
                />
              </div>
              <div>
                <label className="font-semibold text-slate-300 mb-1 block">Bcc</label>
                <input
                  type="text"
                  value={bcc}
                  onChange={(e) => setBcc(e.target.value)}
                  placeholder="bcc@domain.com"
                  disabled={sending}
                  className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
                />
              </div>
            </div>
          )}

          <div>
            <label className="font-semibold text-slate-300 mb-1.5 block">{t("ui.subject")}</label>
            <input
              type="text"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder={t("ui.subject")}
              required
              disabled={sending}
              className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
            />
          </div>

          {/* Formatting Toolbar */}
          <div>
            <label className="font-semibold text-slate-300 mb-1.5 block">{t("ui.messageBody")}</label>
            <div className="border border-slate-800 rounded-xl overflow-hidden bg-slate-900/90">
              <div className="flex items-center gap-1 p-2 bg-slate-950/80 border-b border-slate-800/80 flex-wrap text-slate-300">
                <button
                  type="button"
                  onClick={() => executeFormat("bold")}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-slate-300 hover:text-white"
                  title={t("mail.bold")}
                >
                  <Bold className="w-4 h-4" />
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("italic")}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-slate-300 hover:text-white"
                  title={t("mail.italic")}
                >
                  <Italic className="w-4 h-4" />
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("underline")}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-slate-300 hover:text-white"
                  title={t("mail.underline")}
                >
                  <Underline className="w-4 h-4" />
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("strikeThrough")}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-slate-300 hover:text-white"
                  title={t("mail.strikethrough")}
                >
                  <Strikethrough className="w-4 h-4" />
                </button>
                <span className="w-px h-4 bg-slate-800 mx-1" />
                <button
                  type="button"
                  onClick={() => executeFormat("insertUnorderedList")}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-slate-300 hover:text-white"
                  title={t("mail.bulletList")}
                >
                  <List className="w-4 h-4" />
                </button>
                <button
                  type="button"
                  onClick={() => executeFormat("insertOrderedList")}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-slate-300 hover:text-white"
                  title={t("mail.numberedList")}
                >
                  <ListOrdered className="w-4 h-4" />
                </button>
                <span className="w-px h-4 bg-slate-800 mx-1" />
                <button
                  type="button"
                  onClick={() => {
                    const url = prompt(t("mail.insertUrl"));
                    if (url) executeFormat("createLink", url);
                  }}
                  className="p-1.5 hover:bg-slate-800 rounded-lg text-indigo-400 hover:text-indigo-300"
                  title={t("mail.insertLink")}
                >
                  <LinkIcon className="w-4 h-4" />
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
              <span className="font-semibold text-slate-300">{t("mail.attachments", { count: attachments.length })}</span>
              <label className="cursor-pointer text-indigo-400 hover:text-indigo-300 flex items-center gap-1.5 font-medium">
                <Paperclip className="w-3.5 h-3.5" />
                <span>{t("mail.addFiles")}</span>
                <input
                  type="file"
                  multiple
                  onChange={handleFileUpload}
                  className="hidden"
                />
              </label>
            </div>

            {/* Says so plainly rather than letting someone attach a contract
                and watch it silently not arrive: /mail/send is handed the file
                names only, because there is no upload endpoint behind this
                picker yet. */}
            {attachments.length > 0 && (
              <div className="mb-2 rounded-xl border border-amber-500/30 bg-amber-500/10 p-2.5 text-[11px] text-amber-300">
                {t("mail.attachmentsUnsupported")}
              </div>
            )}

            {attachments.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {attachments.map((file) => (
                  <div
                    key={file.id}
                    className="flex items-center gap-2 px-3 py-1.5 rounded-xl bg-slate-900 border border-slate-800 text-xs text-slate-200"
                  >
                    <Paperclip className="w-3 h-3 text-indigo-400" />
                    <span className="font-medium max-w-[140px] truncate">{file.name}</span>
                    <span className="text-[10px] text-slate-500">
                      ({(file.size / 1024).toFixed(1)} KB)
                    </span>
                    <button
                      type="button"
                      onClick={() => removeAttachment(file.id)}
                      className="text-slate-400 hover:text-rose-400 ml-1"
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="flex items-center justify-between pt-4 border-t border-slate-800">
            <Link href="/user/mail/inbox">
              <Button variant="ghost" size="md">
                {t("mail.discard")}
              </Button>
            </Link>

            <Button type="submit" variant="primary" size="md" disabled={sending}>
              <Send className="w-4 h-4" />
              <span>{sending ? t("mail.sending") : t("mail.sendMessage")}</span>
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}

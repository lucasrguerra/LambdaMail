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
  // The UID of the draft already stored for this message. Sent back on each
  // autosave so the server replaces it instead of leaving another copy.
  const draftUidRef = useRef<number>(0);

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
          replace_uid: draftUidRef.current,
        }),
      });
      if (res.ok) {
        const data = await res.json().catch(() => ({}));
        if (typeof data.uid === "number") draftUidRef.current = data.uid;
      }
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

  /** One row of the compose sheet: a fixed-width label beside the control. */
  const fieldRow = "flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5 shadow-[inset_0_-1px_0_0_rgba(233,233,237,0.07)]";
  const fieldLabel = "w-14 flex-none text-[12.5px] leading-snug text-slate-400";
  const bareInput =
    "min-h-[30px] min-w-[160px] flex-1 border-0 bg-transparent text-sm text-slate-100 placeholder-slate-500 focus:outline-none";
  const toolButton =
    "flex h-8 w-8 items-center justify-center rounded-lg text-slate-300 transition-colors hover:bg-white/[0.07] hover:text-slate-100";

  return (
    <div className="flex-1 overflow-y-auto bg-dark-bg px-5 pb-11 pt-7 sm:px-8">
      <form onSubmit={handleSend} className="mx-auto flex w-full max-w-[860px] flex-col gap-[18px]">
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div className="min-w-0">
            <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("mail.compose")}</h1>
            {draftStatus && (
              <div className="mt-1 flex items-center gap-1.5 text-[12.5px] text-slate-400">
                <CheckCircle2 className="h-3.5 w-3.5 flex-none text-indigo-500" />
                {draftStatus}
              </div>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <Link href="/user/mail/inbox">
              <Button variant="secondary" size="md">
                {t("mail.discard")}
              </Button>
            </Link>
            <Button type="submit" variant="primary" size="md" disabled={sending}>
              <Send className="h-4 w-4" />
              <span>{sending ? t("mail.sending") : t("mail.sendMessage")}</span>
            </Button>
          </div>
        </div>

        {sendError && (
          <div className="rounded-xl bg-rose-900/60 px-4 py-3 text-xs leading-relaxed text-rose-200 shadow-edge">
            {sendError}
          </div>
        )}

        {undoSeconds !== null && (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-dark-card px-4 py-3 text-xs text-slate-200 shadow-edge">
            <div className="flex items-center gap-2">
              <Sparkles className="h-4 w-4 flex-none animate-spin text-indigo-500" />
              {/* The countdown is a placeholder inside the message, not text
                  bolted on after it. It used to be both: the bundle's own
                  "{seconds}" was never substituted and a second, hardcoded
                  count was appended, so this read "Undo Send ({seconds}s
                  remaining) (9s remaining)". */}
              <span>{t("mail.undoSend", { seconds: undoSeconds })}</span>
            </div>
            <Button variant="primary" size="sm" onClick={handleUndo}>
              <Undo2 className="h-3.5 w-3.5" />
              {t("mail.undoSendAction")}
            </Button>
          </div>
        )}

        {/* One sheet, the fields stacked as rows inside it, rather than a stack
            of separately outlined inputs: the message is one object. */}
        <div className="overflow-hidden rounded-2xl bg-dark-panel shadow-edge">
          <div className={fieldRow}>
            <label htmlFor="compose-to" className={fieldLabel}>
              {t("ui.toRecipients")}
            </label>
            <input
              id="compose-to"
              type="text"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              placeholder="recipient@example.com"
              required
              disabled={sending}
              className={bareInput}
            />
            <button
              type="button"
              onClick={() => setShowCcBcc(!showCcBcc)}
              className="flex-none rounded-lg px-2 py-1 text-[12.5px] text-indigo-500 transition-colors hover:bg-indigo-500/10"
            >
              {showCcBcc ? t("mail.hideCcBcc") : t("mail.showCcBcc")}
            </button>
          </div>

          {showCcBcc && (
            <>
              <div className={fieldRow}>
                <label htmlFor="compose-cc" className={fieldLabel}>
                  Cc
                </label>
                <input
                  id="compose-cc"
                  type="text"
                  value={cc}
                  onChange={(e) => setCc(e.target.value)}
                  placeholder="cc@domain.com"
                  disabled={sending}
                  className={bareInput}
                />
              </div>
              <div className={fieldRow}>
                <label htmlFor="compose-bcc" className={fieldLabel}>
                  Bcc
                </label>
                <input
                  id="compose-bcc"
                  type="text"
                  value={bcc}
                  onChange={(e) => setBcc(e.target.value)}
                  placeholder="bcc@domain.com"
                  disabled={sending}
                  className={bareInput}
                />
              </div>
            </>
          )}

          <div className={fieldRow}>
            <label htmlFor="compose-subject" className={fieldLabel}>
              {t("ui.subject")}
            </label>
            <input
              id="compose-subject"
              type="text"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder={t("ui.subject")}
              required
              disabled={sending}
              className={bareInput}
            />
          </div>

          {/* Formatting Toolbar */}
          <div className="flex flex-wrap items-center gap-0.5 px-3 py-2 shadow-[inset_0_-1px_0_0_rgba(233,233,237,0.07)]">
            <button type="button" onClick={() => executeFormat("bold")} className={toolButton} title={t("mail.bold")}>
              <Bold className="h-4 w-4" />
            </button>
            <button type="button" onClick={() => executeFormat("italic")} className={toolButton} title={t("mail.italic")}>
              <Italic className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => executeFormat("underline")}
              className={toolButton}
              title={t("mail.underline")}
            >
              <Underline className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => executeFormat("strikeThrough")}
              className={toolButton}
              title={t("mail.strikethrough")}
            >
              <Strikethrough className="h-4 w-4" />
            </button>
            <span className="mx-1.5 h-[18px] w-px bg-white/[0.14]" />
            <button
              type="button"
              onClick={() => executeFormat("insertUnorderedList")}
              className={toolButton}
              title={t("mail.bulletList")}
            >
              <List className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => executeFormat("insertOrderedList")}
              className={toolButton}
              title={t("mail.numberedList")}
            >
              <ListOrdered className="h-4 w-4" />
            </button>
            <span className="mx-1.5 h-[18px] w-px bg-white/[0.14]" />
            <button
              type="button"
              onClick={() => {
                const url = prompt(t("mail.insertUrl"));
                if (url) executeFormat("createLink", url);
              }}
              className={`${toolButton} text-indigo-500`}
              title={t("mail.insertLink")}
            >
              <LinkIcon className="h-4 w-4" />
            </button>
          </div>

          <div
            ref={editorRef}
            contentEditable
            suppressContentEditableWarning
            aria-label={t("ui.messageBody")}
            role="textbox"
            aria-multiline="true"
            className="min-h-[280px] px-[22px] py-5 font-sans text-[14.5px] leading-relaxed text-slate-200 focus:outline-none"
          />

          {/* Attachment Selector & Display */}
          <div className="flex flex-wrap items-center gap-2.5 px-4 py-3.5 shadow-[inset_0_1px_0_0_rgba(233,233,237,0.07)]">
            <label className="flex flex-none cursor-pointer items-center gap-2 rounded-[10px] border border-white/[0.14] px-3 py-2 text-[13px] text-slate-200 transition-colors hover:bg-white/[0.07]">
              <Paperclip className="h-[15px] w-[15px]" />
              <span>{t("mail.addFiles")}</span>
              <input type="file" multiple onChange={handleFileUpload} className="hidden" />
            </label>

            {attachments.map((file) => (
              <div
                key={file.id}
                className="flex items-center gap-2 rounded-[10px] bg-dark-card px-3 py-2 text-[12.5px] text-slate-200 shadow-edge"
              >
                <Paperclip className="h-[15px] w-[15px] flex-none text-indigo-400" />
                <span className="max-w-[160px] truncate">{file.name}</span>
                <span className="text-[11px] text-slate-500">{(file.size / 1024).toFixed(1)} KB</span>
                <button
                  type="button"
                  onClick={() => removeAttachment(file.id)}
                  className="text-slate-400 transition-colors hover:text-rose-400"
                  aria-label={t("common.delete")}
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}

            {/* Says so plainly rather than letting someone attach a contract
                and watch it silently not arrive: /mail/send is handed the file
                names only, because there is no upload endpoint behind this
                picker yet. */}
            {attachments.length > 0 && (
              <span className="ml-auto text-[11.5px] leading-relaxed text-slate-400">
                {t("mail.attachmentsUnsupported")}
              </span>
            )}
          </div>
        </div>
      </form>
    </div>
  );
}

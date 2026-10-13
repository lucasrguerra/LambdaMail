"use client";

import React, { useState } from "react";
import Link from "next/link";
import { motion } from "framer-motion";
import {
  AlertCircle,
  ArrowLeft,
  ChevronDown,
  Download,
  FolderInput,
  Forward,
  Image as ImageIcon,
  MailOpen,
  Pencil,
  Reply,
  ReplyAll,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { useI18n } from "../i18n/provider";
import { Avatar } from "./Avatar";
import { offersReplyAll, replyAllRecipients, replyRecipients } from "../lib/recipients";

export interface ReaderMessage {
  uid: number;
  subject: string;
  from: string;
  to: string[];
  cc: string[];
  date: string;
  text: string;
  attachments: string[];
  message_id?: string;
  /** The sender asked for priority (Importance: high). */
  important?: boolean;
}

export interface ReaderRow {
  sender_address: string;
  from_display_name: string;
  received_at: string;
  seen: boolean;
  flagged?: boolean;
}

export interface ReaderActions {
  canEdit: boolean;
  canReply: boolean;
  canForward: boolean;
  canMarkUnread: boolean;
  deleteIsPermanent: boolean;
}

interface MoveTarget {
  name: string;
  label: string;
}

interface Props {
  message: ReaderMessage;
  row?: ReaderRow;
  /** The signed-in address, left out of every reply-all. */
  ownAddress: string;
  folder: string;
  actions: ReaderActions;
  moveTargets: MoveTarget[];
  /** The document the body frame renders, or null for a text-only message. */
  document: string | null;
  frameHeight: number;
  /** Whether anything was held back, so the privacy bar is not always shown. */
  hasBlockedContent: boolean;
  imagesLoaded: boolean;
  senderRemembered: boolean;
  onClose: () => void;
  onToggleSeen: () => void;
  onToggleFlagged: () => void;
  onDelete: () => void;
  onMove: (target: string) => void;
  onToggleImages: () => void;
  onToggleRemember: () => void;
}

/** The sender's name and address, from a header like `Jenny <jenny@x.test>`. */
function splitSender(from: string): { name: string; address: string } {
  const match = from.match(/^\s*"?([^"<]*?)"?\s*<([^>]+)>\s*$/);
  if (match) return { name: match[1].trim(), address: match[2].trim() };
  return { name: "", address: from.trim() };
}

/** The initials shown in the sender avatar, from a display name or an address. */
function initialsOf(name: string, address: string): string {
  const source = (name || address || "?").trim();
  const parts = source.replace(/[<>"]/g, "").split(/[\s.@_-]+/).filter(Boolean);
  return (parts.slice(0, 2).map((p) => p[0]).join("") || "?").toUpperCase();
}

/** A short type tag and colour for an attachment, from its extension. */
function attachmentKind(filename: string): { tag: string; className: string } {
  const ext = (filename.split(".").pop() ?? "").toLowerCase();
  if (ext === "pdf") return { tag: "PDF", className: "bg-rose-700" };
  if (["png", "jpg", "jpeg", "gif", "webp", "bmp"].includes(ext)) return { tag: "IMG", className: "bg-sky-700" };
  if (["doc", "docx", "odt", "rtf", "txt"].includes(ext)) return { tag: "DOC", className: "bg-blue-700" };
  if (["xls", "xlsx", "ods", "csv"].includes(ext)) return { tag: "XLS", className: "bg-emerald-700" };
  if (["zip", "rar", "7z", "gz", "tar"].includes(ext)) return { tag: "ZIP", className: "bg-amber-700" };
  return { tag: (ext || "FILE").slice(0, 4).toUpperCase(), className: "bg-slate-600" };
}

// One look for every control on the command bar, so the bar reads as a single
// strip. The label hides on narrow screens and the icon stays, which is what
// keeps six actions on one row at phone width.
const commandClass =
  "inline-flex h-10 min-w-10 items-center justify-center gap-1.5 rounded-lg px-2.5 text-[13px] text-slate-200 transition-colors hover:bg-white/[0.07] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400 md:px-3";
const infoLinkClass =
  "rounded-md px-1.5 py-1 text-[12.5px] font-medium text-indigo-300 transition-colors hover:bg-white/[0.07] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400";

export function MessageReader(props: Props) {
  const { message, row, folder, actions } = props;
  const { t, locale } = useI18n();
  const [moveOpen, setMoveOpen] = useState(false);
  const [recipientsOpen, setRecipientsOpen] = useState(false);

  const { name, address } = splitSender(message.from);
  const displayName = name || row?.from_display_name || address;
  const showAddress = displayName !== address;

  const when = message.date || row?.received_at || "";
  const whenText = when
    ? new Date(when).toLocaleString(locale, {
        weekday: "short",
        day: "numeric",
        month: "short",
        year: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";

  // Collapsed, the recipient line names the first two and counts the rest: a
  // newsletter sent to forty people pushed the message itself off the screen.
  const visibleTo = recipientsOpen ? message.to : message.to.slice(0, 2);
  const hiddenCount = message.to.length - visibleTo.length + (recipientsOpen ? 0 : message.cc.length);
  const showCc = recipientsOpen && message.cc.length > 0;

  const composeHref = (recipients: { to: string[]; cc: string[] }, mode: "reply" | "replyAll") => {
    const query = new URLSearchParams({ mode, subject: message.subject });
    if (recipients.to.length > 0) query.set("replyTo", recipients.to.join(", "));
    if (recipients.cc.length > 0) query.set("cc", recipients.cc.join(", "));
    if (message.message_id) query.set("inReplyTo", message.message_id);
    return `/user/compose?${query.toString()}`;
  };
  const replyHref = composeHref(replyRecipients(message, props.ownAddress), "reply");
  const replyAllHref = composeHref(replyAllRecipients(message, props.ownAddress), "replyAll");
  // Offered only when it would reach someone a plain reply would not: with
  // one other person on the thread the two buttons would do the same thing.
  const canReplyAll = actions.canReply && offersReplyAll(message, props.ownAddress);
  // Forward carries the subject but no recipient: prefilling the original
  // sender would send the message straight back to whoever wrote it.
  const forwardHref = `/user/compose?mode=forward&subject=${encodeURIComponent(message.subject)}`;
  const flagged = Boolean(row?.flagged);
  const deleteLabel = actions.deleteIsPermanent ? t("mail.deleteForever") : t("common.delete");

  return (
    <motion.article
      key={message.uid}
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: -8 }}
      transition={{ duration: 0.15 }}
      className="flex min-h-0 flex-1 flex-col"
    >
      {/* Command bar. It stays put while the message scrolls under it, so
          reply and delete are never a scroll away from a long newsletter. */}
      <div
        role="toolbar"
        aria-label={t("mail.messageActions")}
        className="flex flex-none flex-wrap items-center gap-0.5 bg-dark-bg px-2 py-1.5 shadow-[0_1px_0_0_rgba(233,233,237,0.09)] sm:px-4"
      >
        <button
          type="button"
          onClick={props.onClose}
          className={`${commandClass} md:hidden`}
          title={t("mail.backToList")}
          aria-label={t("mail.backToList")}
        >
          <ArrowLeft className="h-4 w-4" />
        </button>

        {actions.canEdit && (
          <Link href={`/user/compose?draft=${message.uid}`} className={`${commandClass} text-indigo-300`}>
            <Pencil className="h-4 w-4" />
            <span className="hidden xl:inline">{t("mail.continueDraft")}</span>
          </Link>
        )}
        {actions.canReply && (
          <Link href={replyHref} className={`${commandClass} text-indigo-300`} title={t("mail.reply")}>
            <Reply className="h-4 w-4" />
            <span className="hidden xl:inline">{t("mail.reply")}</span>
          </Link>
        )}
        {canReplyAll && (
          <Link href={replyAllHref} className={commandClass} title={t("mail.replyAll")}>
            <ReplyAll className="h-4 w-4" />
            <span className="hidden 2xl:inline">{t("mail.replyAll")}</span>
          </Link>
        )}
        {actions.canForward && (
          <Link href={forwardHref} className={commandClass} title={t("mail.forward")}>
            <Forward className="h-4 w-4" />
            <span className="hidden 2xl:inline">{t("mail.forward")}</span>
          </Link>
        )}

        <span aria-hidden className="mx-1 h-5 w-px bg-white/[0.12]" />

        <button type="button" onClick={props.onDelete} className={commandClass} title={deleteLabel}>
          <Trash2 className="h-4 w-4" />
          <span className="hidden 2xl:inline">{deleteLabel}</span>
        </button>

        <div className="relative">
          <button
            type="button"
            onClick={() => setMoveOpen((open) => !open)}
            aria-expanded={moveOpen}
            aria-haspopup="menu"
            className={commandClass}
            title={t("mail.moveTo")}
          >
            <FolderInput className="h-4 w-4" />
            <span className="hidden 2xl:inline">{t("mail.moveTo")}</span>
            <ChevronDown className="hidden h-3 w-3 2xl:block" />
          </button>
          {moveOpen && (
            <div
              role="menu"
              className="absolute right-0 top-full z-20 mt-1 min-w-[190px] overflow-hidden rounded-xl bg-dark-panel py-1 shadow-edge md:left-0 md:right-auto"
            >
              {props.moveTargets.map((target) => (
                <button
                  key={target.name}
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setMoveOpen(false);
                    props.onMove(target.name);
                  }}
                  className="block w-full px-3.5 py-2.5 text-left text-[13px] text-slate-200 transition-colors hover:bg-white/[0.07] focus-visible:bg-white/[0.07] focus-visible:outline-none"
                >
                  {target.label}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* Not offered in Sent or Drafts: nothing delivers to those folders,
            so an unread flag there says nothing. */}
        {actions.canMarkUnread && (
          <button
            type="button"
            onClick={props.onToggleSeen}
            className={commandClass}
            title={row?.seen === false ? t("mail.markRead") : t("mail.markUnread")}
          >
            <MailOpen className="h-4 w-4" />
            <span className="hidden 2xl:inline">
              {row?.seen === false ? t("mail.markRead") : t("mail.markUnread")}
            </span>
          </button>
        )}

        {/* The importance mark, available in every folder: a message in Sent
            or Archive can be worth flagging too. */}
        <button
          type="button"
          onClick={props.onToggleFlagged}
          aria-pressed={flagged}
          className={`${commandClass} ${flagged ? "text-amber-300" : ""}`}
          title={flagged ? t("mail.unmarkImportant") : t("mail.markImportant")}
        >
          <Star className="h-4 w-4" fill={flagged ? "currentColor" : "none"} />
          <span className="hidden 2xl:inline">{flagged ? t("mail.unmarkImportant") : t("mail.markImportant")}</span>
        </button>

        <button
          type="button"
          onClick={props.onClose}
          className={`${commandClass} ml-auto hidden md:inline-flex`}
          title={t("mail.backToList")}
          aria-label={t("mail.backToList")}
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[1000px] flex-col gap-4 px-4 pb-10 pt-5 sm:px-7 2xl:max-w-[1140px]">
          {message.important && (
            <span className="inline-flex w-fit items-center gap-1.5 rounded-full bg-rose-900/60 px-2.5 py-0.5 text-xs font-medium text-rose-200 shadow-[inset_0_0_0_1px_rgba(244,63,94,0.45)]">
              <AlertCircle className="h-3.5 w-3.5" aria-hidden />
              {t("mail.highImportanceBadge")}
            </span>
          )}
          <h1 className="text-[19px] font-semibold leading-snug tracking-[-0.01em] text-slate-100 [overflow-wrap:anywhere] [text-wrap:pretty] sm:text-[22px]">
            {message.subject || t("mail.noSubject")}
          </h1>

          {/* The sender block: name first and the address second, because the
              name is what a reader recognises and a long address used to be
              the loudest thing in the header. */}
          <header className="flex items-start gap-3">
            <Avatar
              address={row?.sender_address ?? address}
              initials={initialsOf(displayName, address)}
              size={44}
              className="bg-indigo-900 text-indigo-300 shadow-[inset_0_0_0_1px_#5d5294]"
            />
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                <span className="text-[15px] font-semibold text-slate-100 [overflow-wrap:anywhere]">{displayName}</span>
                {showAddress && (
                  <span className="text-[13px] text-slate-400 [overflow-wrap:anywhere]">&lt;{address}&gt;</span>
                )}
              </div>
              <div className="text-[12.5px] leading-relaxed text-slate-400 [overflow-wrap:anywhere]">
                {visibleTo.length > 0 && (
                  <>
                    <span className="text-slate-500">{t("ui.toRecipients")}: </span>
                    {visibleTo.join(", ")}
                  </>
                )}
                {showCc && (
                  <div>
                    <span className="text-slate-500">{t("ui.ccRecipients")}: </span>
                    {message.cc.join(", ")}
                  </div>
                )}
                {(hiddenCount > 0 || recipientsOpen) && (
                  <button
                    type="button"
                    onClick={() => setRecipientsOpen((open) => !open)}
                    aria-expanded={recipientsOpen}
                    className="ml-1 inline-flex items-center gap-0.5 rounded px-1 text-indigo-300 hover:bg-white/[0.07] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
                  >
                    {recipientsOpen ? t("mail.showFewerRecipients") : `+${hiddenCount}`}
                    <ChevronDown className={`h-3 w-3 transition-transform ${recipientsOpen ? "rotate-180" : ""}`} />
                  </button>
                )}
              </div>
              {/* On a phone the date drops under the recipients rather than
                  squeezing the name. */}
              <span className="text-xs tabular-nums text-slate-500 sm:hidden">{whenText}</span>
            </div>
            <span className="hidden flex-none pt-0.5 text-xs tabular-nums text-slate-400 sm:block">{whenText}</span>
          </header>

          {/* One quiet line, shown only when something was actually held back.
              The wording is the reassurance, not the colour: this is not an
              error. Two separate decisions - look now, or stop asking about
              this sender - so a reader can see one message without granting
              anything permanently. */}
          {(props.hasBlockedContent || props.senderRemembered) && (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg bg-dark-card px-3 py-2 shadow-edge">
              <ImageIcon className="h-4 w-4 flex-none text-indigo-400" aria-hidden />
              <span className="min-w-[180px] flex-1 text-[12.5px] leading-relaxed text-slate-300">
                {props.imagesLoaded ? t("mail.imagesEnabled") : t("mail.remoteContentBlocked")}
              </span>
              <button type="button" onClick={props.onToggleImages} className={infoLinkClass}>
                {props.imagesLoaded ? t("mail.hideImages") : t("mail.loadImages")}
              </button>
              <button type="button" onClick={props.onToggleRemember} className={infoLinkClass}>
                {props.senderRemembered ? t("mail.forgetSender") : t("mail.alwaysFromSender")}
              </button>
            </div>
          )}

          {message.attachments.length > 0 && (
            <section aria-label={t("mail.attachments", { count: message.attachments.length })}>
              <div className="flex gap-2 overflow-x-auto pb-1">
                {message.attachments.map((filename, idx) => {
                  const kind = attachmentKind(filename);
                  return (
                    <a
                      key={`${filename}-${idx}`}
                      href={`/api/v1/mail/message/${message.uid}/attachment/${encodeURIComponent(filename)}?folder=${encodeURIComponent(folder)}`}
                      download={filename}
                      className="group flex h-10 max-w-[260px] flex-none items-center gap-2 rounded-lg bg-dark-card px-2.5 text-slate-200 shadow-edge transition-shadow hover:shadow-edge-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
                    >
                      <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold text-white ${kind.className}`}>
                        {kind.tag}
                      </span>
                      <span className="min-w-0 truncate text-[12.5px]">{filename}</span>
                      <Download className="h-3.5 w-3.5 flex-none text-slate-500 group-hover:text-indigo-300" />
                    </a>
                  );
                })}
              </div>
            </section>
          )}

          {props.document ? (
            <div className="overflow-hidden rounded-xl bg-white shadow-edge">
              <iframe
                title={t("ui.messageBody")}
                srcDoc={props.document}
                /* allow-scripts, and deliberately not allow-same-origin: the
                   frame stays on an opaque origin - it cannot read a cookie
                   or touch this document - while still running the few lines
                   that report its height and open links outward. Message
                   markup is sanitised before it gets here. */
                sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
                referrerPolicy="no-referrer"
                style={{ height: `${props.frameHeight}px` }}
                className="block w-full border-0"
              />
            </div>
          ) : (
            <div className="rounded-xl bg-dark-panel px-5 py-5 shadow-edge sm:px-6">
              <pre className="whitespace-pre-wrap break-words font-sans text-[14.5px] leading-relaxed text-slate-200">
                {message.text || t("mail.emptyBody")}
              </pre>
            </div>
          )}
        </div>
      </div>
    </motion.article>
  );
}

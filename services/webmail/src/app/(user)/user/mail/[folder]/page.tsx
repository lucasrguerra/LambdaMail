"use client";

import React, { use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { motion, AnimatePresence } from "framer-motion";
import {
  Search,
  Star,
  Paperclip,
  Inbox,
  Mail,
  Clock,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { useI18n } from "../../../../../i18n/provider";
import { useMailEvents } from "../../../../../lib/useMailEvents";
import { useFolders, notifyMailStateChanged } from "../../../../../lib/useFolders";
import {
  folderMetrics,
  filterMessages,
  messageCounts,
  listHeaderCount,
  applySeen,
  applyFlagged,
  readerActions,
  moveTargets,
  type ListFilter,
} from "../../../../../lib/mailCounts";
import { sanitizeEmailHtml, blockRemoteImages, unblockRemoteImages } from "../../../../../lib/sanitizer";
import { isSenderTrusted, trustSender, revokeSender } from "../../../../../lib/remoteImages";
import { resolveInlineImages, buildReaderDocument, type InlineImages } from "../../../../../lib/emailBody";
import { Button } from "../../../../../components/ui/Button";
import {
  clampListWidth,
  DEFAULT_LIST_WIDTH,
  readStoredWidth,
  storeWidth,
} from "../../../../../lib/paneWidth";
import { useAccount } from "../../../../../lib/useAccount";
import { MessageReader } from "../../../../../components/MessageReader";
import { Avatar } from "../../../../../components/Avatar";

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
  flagged: boolean;
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
  message_id?: string;
  important?: boolean;
  // The message's own pictures, keyed by Content-ID. Without these every
  // cid: reference in the body is unresolvable and renders as a broken icon.
  inline_images: InlineImages;
}

/** The initials shown in the sender avatar, from a display name or an address. */
function initials(name: string, address: string): string {
  const source = (name || address || "?").trim();
  const parts = source.replace(/[<>"]/g, "").split(/[\s.@_-]+/).filter(Boolean);
  const letters = parts.slice(0, 2).map((p) => p[0]);
  return (letters.join("") || "?").toUpperCase();
}

/**
 * A timestamp as a mail client shows one: the time for today, the date for
 * anything older. A fixed time-only format made every message in the list look
 * like it arrived today.
 */
function listTimestamp(iso: string, locale: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const now = new Date();
  const sameDay =
    date.getDate() === now.getDate() &&
    date.getMonth() === now.getMonth() &&
    date.getFullYear() === now.getFullYear();
  return sameDay
    ? date.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" })
    : date.toLocaleDateString(locale, { day: "2-digit", month: "short" });
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

export default function MailFolderPage({ params }: { params: Promise<{ folder: string }> }) {
  const { folder } = use(params);
  // The locale is needed alongside the translator: timestamps are formatted
  // against it, and a fixed format is why the list rendered every date the same
  // way whatever language the interface was in.
  const { t, locale } = useI18n();

  const [messages, setMessages] = useState<MessageSummary[]>([]);
  const [selected, setSelected] = useState<RenderedMessage | null>(null);
  const [loadRemoteImages, setLoadRemoteImages] = useState(false);
  // Whether this sender has a standing decision, so the banner can offer to
  // take it back rather than only to grant it.
  const [rememberSender, setRememberSender] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [activeFilter, setActiveFilter] = useState<ListFilter>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The frame is sized from the message rather than pinned to a guess: 500px
  // clipped every long message and left white space under every short one.
  const [readerHeight, setReaderHeight] = useState(420);

  const account = useAccount("user");
  // The list's width, draggable on a wide screen like the Outlook divider.
  // Remembered between visits; below md the panes stack and it does not apply.
  const [listWidth, setListWidth] = useState(DEFAULT_LIST_WIDTH);
  const [dragging, setDragging] = useState(false);
  const paneRowRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const stored = readStoredWidth();
    if (stored !== null) setListWidth(stored);
  }, []);

  /** Applies a width within the limits the current window allows. */
  const resizeList = useCallback((width: number, persist: boolean) => {
    const container = paneRowRef.current?.getBoundingClientRect().width ?? window.innerWidth;
    const next = clampListWidth(width, container);
    setListWidth(next);
    if (persist) storeWidth(next);
  }, []);

  const { folders } = useFolders();
  const metrics = folderMetrics(folders, folder);
  const router = useRouter();
  // Which buttons this folder's messages deserve. The reader offered the same
  // four everywhere, so Sent and Drafts got a mark-as-unread that means
  // nothing there, drafts got Reply instead of a way to finish writing, and
  // nothing anywhere got a delete.
  const actions = readerActions(folder);

  const loadMessages = useCallback(
    async (search: string) => {
      setLoading(true);
      setError(null);
      try {
        const query = new URLSearchParams({ folder, ...(search ? { q: search } : {}) });
        const res = await fetch(`/api/v1/mail/messages?${query}`, { cache: "no-store" });
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
    [folder, t]
  );

  useMailEvents(
    useCallback(
      (event) => {
        if (event.type === "resync" || event.type === "EmailReceived") {
          void loadMessages(searchQuery);
        }
      },
      [loadMessages, searchQuery]
    )
  );

  // One effect drives both the first load and the search, debounced. Having a
  // separate mount effect as well meant every folder change fired two requests
  // and the later one could be the stale answer.
  useEffect(() => {
    const handle = setTimeout(() => void loadMessages(searchQuery), searchQuery ? 250 : 0);
    return () => clearTimeout(handle);
  }, [searchQuery, loadMessages]);

  // The reader frame reports its own height; nothing outside a sandboxed frame
  // can measure it.
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const data = event.data as { type?: string; height?: number } | null;
      if (data?.type !== "lm:reader-height" || typeof data.height !== "number") return;
      // Clamped so a message that reports something absurd cannot produce a
      // frame kilometres tall or one collapsed to nothing.
      setReaderHeight(Math.min(20000, Math.max(200, Math.ceil(data.height))));
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  /**
   * Records a read-state change on the server and everywhere it is displayed.
   *
   * Opening a message already marked it read server-side, but the list held its
   * own optimistic copy and the sidebar badge held another, so the three
   * disagreed until the page was reloaded.
   */
  const setSeen = useCallback(
    async (uid: number, seen: boolean, { persist }: { persist: boolean }) => {
      setMessages((current) => applySeen(current, uid, seen));
      notifyMailStateChanged();
      if (!persist) return;
      try {
        await fetch("/api/v1/mail/seen", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ folder, uid, seen }),
        });
      } catch {
        // The server is the authority; the next push or reload corrects this.
      } finally {
        notifyMailStateChanged();
      }
    },
    [folder]
  );

  /**
   * Marks a message important, or takes the mark off.
   *
   * Optimistic like read state: the star changes at the click and goes back if
   * the server refuses, so a failed request is never left looking like a
   * success.
   */
  const setFlagged = useCallback(
    async (uid: number, flagged: boolean) => {
      setMessages((current) => applyFlagged(current, uid, flagged));
      try {
        const res = await fetch("/api/v1/mail/flagged", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ folder, uid, flagged }),
        });
        if (!res.ok) throw new Error(t("errors.serverError"));
      } catch (err) {
        setMessages((current) => applyFlagged(current, uid, !flagged));
        setError(err instanceof Error ? err.message : t("errors.serverError"));
      }
    },
    [folder, t]
  );

  /**
   * Deletes one message: to Trash from an ordinary folder, and for good from
   * Trash itself, which is what the server does with the same call.
   *
   * The webmail had no delete at all - no button and no route behind one - so
   * anything the user wanted rid of stayed. The draft a sent message left
   * behind was the case that made it impossible to ignore.
   */
  const deleteMessage = useCallback(
    async (uid: number) => {
      // Removed from the list first so the row goes at the moment of the
      // click; the reload below is what makes the server's answer final.
      setMessages((current) => current.filter((m) => m.uid !== uid));
      setSelected((current) => (current?.uid === uid ? null : current));
      try {
        const res = await fetch("/api/v1/mail/delete", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ folder, uid }),
        });
        if (!res.ok) throw new Error(t("errors.serverError"));
      } catch (err) {
        setError(err instanceof Error ? err.message : t("errors.serverError"));
        // The optimistic removal was wrong, so put the folder back as it is.
        void loadMessages(searchQuery);
      } finally {
        // Both the source folder and Trash moved, so the badges must re-read.
        notifyMailStateChanged();
      }
    },
    [folder, t, loadMessages, searchQuery]
  );

  /** Files one message into another folder. */
  const moveMessage = useCallback(
    async (uid: number, target: string) => {
      setMessages((current) => current.filter((m) => m.uid !== uid));
      setSelected((current) => (current?.uid === uid ? null : current));
      try {
        const res = await fetch("/api/v1/mail/move", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ folder, uid, target }),
        });
        if (!res.ok) throw new Error(t("errors.serverError"));
      } catch (err) {
        setError(err instanceof Error ? err.message : t("errors.serverError"));
        void loadMessages(searchQuery);
      } finally {
        // Both folders changed, so the badges have to re-read.
        notifyMailStateChanged();
      }
    },
    [folder, t, loadMessages, searchQuery]
  );

  const openMessage = async (uid: number) => {
    // A draft is an unfinished message: opening it means carrying on writing,
    // not reading it in a pane that offers to reply to yourself.
    if (actions.canEdit) {
      router.push(`/user/compose?draft=${uid}`);
      return;
    }
    setLoadRemoteImages(false);
    setRememberSender(false);
    setReaderHeight(420);
    try {
      const res = await fetch(`/api/v1/mail/message/${uid}?folder=${encodeURIComponent(folder)}`, {
        cache: "no-store",
      });
      if (!res.ok) throw new Error(t("errors.serverError"));
      const message = await res.json();
      setSelected(message);
      // A sender the reader has already allowed does not get asked again.
      const trusted = isSenderTrusted(message.from ?? "");
      setLoadRemoteImages(trusted);
      setRememberSender(trusted);
      // Fetching the message is what marks it read on the server, so this only
      // brings the list and the badges into line with what already happened.
      void setSeen(uid, true, { persist: false });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("errors.serverError"));
    }
  };

  const filteredMessages = filterMessages(messages, activeFilter);
  const counts = messageCounts(messages);
  const header = listHeaderCount({
    folder: metrics,
    loaded: counts.loaded,
    searching: searchQuery.trim().length > 0,
  });
  const selectedRow = messages.find((m) => m.uid === selected?.uid);

  /**
   * The document the reader frame renders.
   *
   * Order matters: sanitise first so nothing downstream handles untrusted
   * markup, then resolve the message's own inline parts, then block or restore
   * remote sources - which is the only part the reader chooses.
   */
  const readerDocument = useMemo(() => {
    if (!selected) return null;
    if (!selected.html) return null;
    const sanitized = sanitizeEmailHtml(selected.html);
    if (!sanitized) return null;
    const withInline = resolveInlineImages(sanitized, selected.inline_images ?? {});
    const body = loadRemoteImages ? unblockRemoteImages(withInline) : blockRemoteImages(withInline);
    return buildReaderDocument(body);
  }, [selected, loadRemoteImages]);

  // Whether anything was actually held back, so the privacy banner is not shown
  // over a message that has no remote content at all.
  const hasBlockedContent = useMemo(() => {
    if (!selected?.html) return false;
    const sanitized = resolveInlineImages(
      sanitizeEmailHtml(selected.html),
      selected.inline_images ?? {}
    );
    return blockRemoteImages(sanitized) !== sanitized;
  }, [selected]);

  const chips: { key: ListFilter; label: string; count: number }[] = [
    { key: null, label: t("mail.allMessages"), count: header.count },
    { key: "unread", label: t("mail.unreadFilter"), count: counts.unread },
    { key: "attachment", label: t("mail.attachmentsFilter"), count: counts.withAttachments },
    { key: "important", label: t("mail.importantFilter"), count: counts.important },
  ];


  // The folder's own name, translated. The heading printed the URL segment
  // capitalised, so an interface running in Portuguese still said "Inbox".
  const FOLDER_LABELS: Record<string, string> = {
    inbox: t("mail.inbox"),
    sent: t("mail.sent"),
    drafts: t("mail.drafts"),
    archive: t("mail.archive"),
    junk: t("mail.junk"),
    trash: t("mail.trash"),
    reports: t("mail.reports"),
  };
  const folderTitle = FOLDER_LABELS[folder.toLowerCase()] ?? folder;

  return (
    <div ref={paneRowRef} className="flex flex-1 overflow-hidden">
      {/* Messages List Column */}
      <div
        style={{ "--list-w": `${listWidth}px` } as React.CSSProperties}
        className={`${selected ? "hidden md:flex" : "flex"} w-full flex-col bg-dark-bg md:w-[var(--list-w)] md:flex-none`}
      >
        {/* Folder heading, with the counts that belong to the folder rather
            than to the page of it that happens to be loaded. */}
        <div className="flex flex-col gap-3 px-4 pb-3 pt-4">
          <div className="flex items-end justify-between gap-3">
            <div className="min-w-0">
              <h2 className="text-xl font-medium leading-tight text-slate-100">{folderTitle}</h2>
              <div className="mt-1 text-xs tabular-nums text-slate-400">
                {header.isFolderTotal
                  ? t("mail.messagesInFolder", { count: header.count })
                  : t("mail.searchMatches", { count: header.count })}
                {metrics.unread > 0 && (
                  <span className="text-indigo-300"> &middot; {t("mail.unreadCount", { count: metrics.unread })}</span>
                )}
              </div>
            </div>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void loadMessages(searchQuery)}
              className="flex-none rounded-[9px]"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
              <span>{t("common.refresh")}</span>
            </Button>
          </div>

          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              type="search"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder={t("mail.searchPlaceholder")}
              aria-label={t("common.search")}
              className="min-h-[38px] w-full rounded-xl bg-dark-panel py-2 pl-[34px] pr-3 text-[13px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent"
            />
          </div>

          {/* Every chip carries its own number: two of the three used to filter
              to a count nothing on screen disclosed. Chips wrap rather than
              scroll, so a longer translation is never pushed out of reach. */}
          <div className="flex flex-wrap gap-1.5">
            {chips.map((chip) => (
              <button
                key={chip.label}
                type="button"
                aria-pressed={activeFilter === chip.key}
                data-active={activeFilter === chip.key}
                onClick={() => setActiveFilter(chip.key)}
                className="lm-chip"
              >
                <span>{chip.label}</span>
                <span className="tabular-nums opacity-70">{chip.count}</span>
              </button>
            ))}
          </div>
        </div>

        {/* Message Items List */}
        <div className="flex flex-1 flex-col gap-[3px] overflow-y-auto px-2 pb-3">
          {loading ? (
            <div className="flex items-center justify-center gap-2 p-6 text-center text-xs text-slate-400">
              <Clock className="h-4 w-4 animate-spin text-indigo-500" />
              <span>{t("common.loading")}</span>
            </div>
          ) : error ? (
            <div className="m-2 rounded-xl bg-rose-900/60 p-3.5 text-xs leading-relaxed text-rose-200 shadow-edge">
              {error}
            </div>
          ) : filteredMessages.length === 0 ? (
            <div className="flex flex-col items-center gap-2 p-8 text-center text-xs text-slate-400">
              <Inbox className="h-8 w-8 text-slate-600" strokeWidth={1.25} />
              <span>{searchQuery ? t("mail.noMatches") : t("mail.noMessages")}</span>
            </div>
          ) : (
            filteredMessages.map((msg) => {
              const isSelected = selected?.uid === msg.uid;
              return (
                /* The row and its delete control are siblings inside a
                   positioned wrapper rather than nested: a button inside a
                   button is invalid markup, and the browser drops the inner
                   one, so the delete would never have been clickable. */
                <div key={msg.uid} className="group relative">
                <button
                  onClick={() => void openMessage(msg.uid)}
                  data-active={isSelected}
                  className="lm-row w-full text-left"
                >
                  <Avatar
                    address={msg.sender_address}
                    initials={initials(msg.from_display_name, msg.sender_address)}
                    size={38}
                    className={
                      msg.seen
                        ? "bg-dark-card text-slate-400"
                        : "bg-indigo-900 text-indigo-300 shadow-[inset_0_0_0_1px_#5d5294]"
                    }
                  />

                  <span className="flex min-w-0 flex-1 flex-col gap-[3px]">
                    <span className="flex items-baseline gap-2">
                      <span
                        className={`min-w-0 truncate text-[13.5px] ${
                          msg.seen ? "text-slate-300" : "text-slate-100"
                        }`}
                      >
                        {msg.from_display_name || msg.sender_address}
                      </span>
                      <span className="ml-auto flex-none text-[11px] tabular-nums text-slate-500">
                        {listTimestamp(msg.received_at, locale)}
                      </span>
                    </span>
                    {/* The subject wraps instead of being clipped - a truncated
                        subject is the one thing a mail list must not do.
                        overflow-wrap:anywhere as well as wrapping, because a
                        report's Message-ID is a single unbroken "word" longer
                        than the column: with nowhere to break it, it ran
                        straight out of the row. Clamped so one such subject
                        cannot push the rest of the list off the screen. */}
                    <span
                      className={`line-clamp-3 text-[13.5px] leading-snug [overflow-wrap:anywhere] ${
                        msg.seen ? "text-slate-300" : "text-slate-100"
                      }`}
                    >
                      {msg.subject || t("mail.noSubject")}
                    </span>
                    <span className="line-clamp-2 text-xs leading-relaxed text-slate-400 [overflow-wrap:anywhere]">
                      {msg.snippet}
                    </span>
                    {msg.has_attachments && (
                      <span className="mt-px flex items-center gap-1.5">
                        <span className="inline-flex items-center gap-1 rounded-md bg-slate-800 px-2 py-0.5 text-[10.5px] text-slate-300">
                          <Paperclip className="h-2.5 w-2.5" />
                          {/* The list knows only that a row has attachments,
                              not how many, so it names the fact rather than
                              printing a count it does not have. */}
                          {t("mail.attachmentsFilter")}
                        </span>
                      </span>
                    )}
                  </span>

                  {/* An unread row is marked by a dot in a reserved column, so
                      the subject does not shift sideways when it is read. */}
                  {msg.flagged && (
                    <Star
                      className="mt-1 h-3.5 w-3.5 flex-none text-amber-300"
                      fill="currentColor"
                      aria-label={t("mail.markedImportant")}
                    />
                  )}
                  <span className="mt-3 w-[7px] flex-none">
                    {!msg.seen && <span className="block h-[7px] w-[7px] rounded-full bg-indigo-500" />}
                  </span>
                </button>
                {/* Reachable without opening the message first, which is what
                    the leftover empty draft needed: there was nothing worth
                    reading in it, and no other way to get rid of it. */}
                <button
                  type="button"
                  onClick={() => void deleteMessage(msg.uid)}
                  title={t("common.delete")}
                  aria-label={t("common.delete")}
                  className="absolute right-2 top-2 hidden h-7 w-7 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-white/[0.09] hover:text-slate-100 focus:flex focus-visible:flex group-hover:flex"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
                </div>
              );
            })
          )}
        </div>
      </div>

      {/* The divider between the panes. Wide screens only: below md they stack
          and there is nothing to divide. A pointer drag, the arrow keys and a
          double-click to restore all move it, so it is not mouse-only. */}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t("mail.resizeList")}
        aria-valuenow={listWidth}
        tabIndex={0}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          setDragging(true);
        }}
        onPointerMove={(e) => {
          if (!dragging) return;
          const left = paneRowRef.current?.getBoundingClientRect().left ?? 0;
          resizeList(e.clientX - left, false);
        }}
        onPointerUp={(e) => {
          e.currentTarget.releasePointerCapture(e.pointerId);
          setDragging(false);
          storeWidth(listWidth);
        }}
        onDoubleClick={() => resizeList(DEFAULT_LIST_WIDTH, true)}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") resizeList(listWidth - 24, true);
          else if (e.key === "ArrowRight") resizeList(listWidth + 24, true);
          else if (e.key === "Home") resizeList(0, true);
          else if (e.key === "End") resizeList(Infinity, true);
          else return;
          e.preventDefault();
        }}
        className={`group relative hidden w-1.5 flex-none cursor-col-resize touch-none select-none md:block ${
          dragging ? "bg-indigo-500/40" : "bg-white/[0.07] hover:bg-indigo-500/30"
        } focus-visible:bg-indigo-500/50 focus-visible:outline-none`}
      >
        {/* A wider invisible target, so the thin line is easy to grab. */}
        <span className="absolute inset-y-0 -left-1.5 -right-1.5" />
      </div>

      {/* Message Reader Column */}
      <div className={`${selected ? "flex" : "hidden md:flex"} min-w-0 flex-1 flex-col overflow-hidden bg-dark-bg`}>
        <AnimatePresence mode="wait">
          {selected ? (
            <MessageReader
              key={selected.uid}
              message={selected}
              row={selectedRow}
              ownAddress={account?.email ?? ""}
              folder={folder}
              actions={actions}
              moveTargets={moveTargets(folders, folder).map((target) => ({
                name: target.name,
                label: FOLDER_LABELS[(target.special_use || target.name).toLowerCase()] ?? target.name,
              }))}
              document={readerDocument}
              frameHeight={readerHeight}
              hasBlockedContent={hasBlockedContent}
              imagesLoaded={loadRemoteImages}
              senderRemembered={rememberSender}
              onClose={() => setSelected(null)}
              onToggleFlagged={() => void setFlagged(selected.uid, !(selectedRow?.flagged ?? false))}
              onToggleSeen={() => void setSeen(selected.uid, !(selectedRow?.seen ?? true), { persist: true })}
              onDelete={() => void deleteMessage(selected.uid)}
              onMove={(target) => void moveMessage(selected.uid, target)}
              onToggleImages={() => setLoadRemoteImages(!loadRemoteImages)}
              onToggleRemember={() => {
                if (rememberSender) {
                  revokeSender(selected.from);
                  setRememberSender(false);
                  setLoadRemoteImages(false);
                } else {
                  trustSender(selected.from);
                  setRememberSender(true);
                  setLoadRemoteImages(true);
                }
              }}
            />
          ) : (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-20 text-center text-sm text-slate-400"
            >
              <Mail className="h-12 w-12 text-slate-600" strokeWidth={1} />
              {/* This pane used to say "No messages" even with a full list
                  beside it: the empty reader is not an empty folder. */}
              <span>{t("mail.selectAMessage")}</span>
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </div>
  );
}

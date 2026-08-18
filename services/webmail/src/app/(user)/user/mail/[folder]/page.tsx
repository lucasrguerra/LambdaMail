"use client";

import React, { use, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { motion, AnimatePresence } from "framer-motion";
import {
  Search,
  Paperclip,
  Reply,
  Forward,
  Inbox,
  Image as ImageIcon,
  Mail,
  Clock,
  MailOpen,
  Download,
  X,
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
  type ListFilter,
} from "../../../../../lib/mailCounts";
import { sanitizeEmailHtml, blockRemoteImages, unblockRemoteImages } from "../../../../../lib/sanitizer";
import { resolveInlineImages, buildReaderDocument, type InlineImages } from "../../../../../lib/emailBody";
import { Button } from "../../../../../components/ui/Button";

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
  const [searchQuery, setSearchQuery] = useState("");
  const [activeFilter, setActiveFilter] = useState<ListFilter>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The frame is sized from the message rather than pinned to a guess: 500px
  // clipped every long message and left white space under every short one.
  const [readerHeight, setReaderHeight] = useState(420);

  const { folders } = useFolders();
  const metrics = folderMetrics(folders, folder);

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

  const openMessage = async (uid: number) => {
    setLoadRemoteImages(false);
    setReaderHeight(420);
    try {
      const res = await fetch(`/api/v1/mail/message/${uid}?folder=${encodeURIComponent(folder)}`, {
        cache: "no-store",
      });
      if (!res.ok) throw new Error(t("errors.serverError"));
      setSelected(await res.json());
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
  ];

  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Messages List Column */}
      <div
        className={`${selected ? "hidden md:flex" : "flex"} w-full md:w-[22rem] lg:w-96 border-r border-slate-800/80 flex-col bg-slate-900/40 backdrop-blur-md md:flex-shrink-0`}
      >
        {/* Folder heading, with the counts that belong to the folder rather
            than to the page of it that happens to be loaded. */}
        <div className="px-3.5 pt-3.5 flex items-baseline justify-between gap-2">
          <h2 className="text-sm font-bold text-slate-100 capitalize truncate">{folder}</h2>
          <span className="text-[11px] text-slate-400 font-mono tabular-nums flex-shrink-0">
            {header.isFolderTotal
              ? t("mail.messagesInFolder", { count: header.count })
              : t("mail.searchMatches", { count: header.count })}
            {metrics.unread > 0 && (
              <span className="text-indigo-300"> &middot; {t("mail.unreadCount", { count: metrics.unread })}</span>
            )}
          </span>
        </div>

        {/* Search Bar & Filter Chips */}
        <div className="p-3.5 border-b border-slate-800 space-y-2.5">
          <div className="relative">
            <Search className="w-4 h-4 absolute left-3 top-3 text-slate-400" />
            <input
              type="search"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder={t("mail.searchPlaceholder")}
              aria-label={t("common.search")}
              className="w-full pl-9 pr-3 py-2 text-xs rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
            />
          </div>

          <div className="flex items-center gap-1.5 text-[11px] overflow-x-auto pb-0.5">
            {chips.map((chip) => (
              <button
                key={chip.label}
                type="button"
                aria-pressed={activeFilter === chip.key}
                onClick={() => setActiveFilter(chip.key)}
                className={`px-2.5 py-1 rounded-lg font-medium transition-all whitespace-nowrap flex items-center gap-1.5 ${
                  activeFilter === chip.key
                    ? "bg-indigo-600 text-white shadow-sm"
                    : "bg-slate-800/60 text-slate-400 hover:text-slate-200"
                }`}
              >
                <span>{chip.label}</span>
                {/* Every chip carries its own number: two of the three used to
                    filter to a count nothing on screen disclosed. */}
                <span className="tabular-nums opacity-80">{chip.count}</span>
              </button>
            ))}
          </div>
        </div>

        {/* Message Items List */}
        <div className="flex-1 overflow-y-auto divide-y divide-slate-800/50">
          {loading ? (
            <div className="p-6 text-center text-xs text-slate-500 animate-pulse flex items-center justify-center gap-2">
              <Clock className="w-4 h-4 text-indigo-400 animate-spin" />
              <span>{t("common.loading")}</span>
            </div>
          ) : error ? (
            <div className="p-4 text-xs text-rose-400 bg-rose-500/10 m-3 rounded-xl border border-rose-500/20">
              {error}
            </div>
          ) : filteredMessages.length === 0 ? (
            <div className="p-8 text-center text-xs text-slate-500 flex flex-col items-center gap-2">
              <Inbox className="w-8 h-8 text-slate-600" />
              <span>{searchQuery ? t("mail.noMatches") : t("mail.noMessages")}</span>
            </div>
          ) : (
            filteredMessages.map((msg) => {
              const isSelected = selected?.uid === msg.uid;
              return (
                <button
                  key={msg.uid}
                  onClick={() => void openMessage(msg.uid)}
                  className={`w-full text-left p-3.5 transition-all duration-150 flex gap-3 relative ${
                    isSelected ? "bg-indigo-600/15" : "hover:bg-slate-800/30"
                  }`}
                >
                  {isSelected && <span className="absolute left-0 top-0 bottom-0 w-[3px] bg-indigo-500" />}
                  {/* An unread row is marked by a dot in a reserved column, so
                      the subject does not shift sideways when it is read. */}
                  <span className="mt-1.5 w-2 flex-shrink-0">
                    {!msg.seen && <span className="block w-2 h-2 rounded-full bg-indigo-400" />}
                  </span>
                  <span
                    className={`mt-0.5 w-8 h-8 rounded-full flex-shrink-0 flex items-center justify-center text-[10px] font-bold ${
                      msg.seen
                        ? "bg-slate-800 text-slate-400"
                        : "bg-indigo-500/25 text-indigo-200 border border-indigo-500/30"
                    }`}
                    aria-hidden="true"
                  >
                    {initials(msg.from_display_name, msg.sender_address)}
                  </span>

                  <span className="min-w-0 flex-1">
                    <span className="flex items-center justify-between gap-2 mb-0.5">
                      <span
                        className={`text-xs truncate ${
                          msg.seen ? "text-slate-300 font-medium" : "text-white font-bold"
                        }`}
                      >
                        {msg.from_display_name || msg.sender_address}
                      </span>
                      <span className="text-[10px] text-slate-500 font-mono flex-shrink-0 tabular-nums">
                        {listTimestamp(msg.received_at, locale)}
                      </span>
                    </span>
                    <span
                      className={`text-xs truncate flex items-center gap-1.5 mb-0.5 ${
                        msg.seen ? "text-slate-300" : "font-semibold text-slate-100"
                      }`}
                    >
                      <span className="truncate">{msg.subject || t("mail.noSubject")}</span>
                      {msg.has_attachments && (
                        <Paperclip className="w-3 h-3 text-slate-400 flex-shrink-0" />
                      )}
                    </span>
                    <span className="block text-[11px] text-slate-500 truncate leading-relaxed">
                      {msg.snippet}
                    </span>
                  </span>
                </button>
              );
            })
          )}
        </div>
      </div>

      {/* Message Reader Column */}
      <div
        className={`${selected ? "flex" : "hidden md:flex"} flex-1 flex-col bg-dark-bg overflow-y-auto`}
      >
        <AnimatePresence mode="wait">
          {selected ? (
            <motion.article
              key={selected.uid}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -8 }}
              transition={{ duration: 0.15 }}
              className="w-full max-w-3xl mx-auto px-4 sm:px-6 py-5 space-y-5"
            >
              {/* Toolbar: closing, replying and read state all in one row, so
                  the reader is not the only pane with no way out of itself. */}
              <div className="flex items-center justify-between gap-2">
                <button
                  type="button"
                  onClick={() => setSelected(null)}
                  className="flex items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-900/70 px-2.5 py-1.5 text-xs text-slate-300 hover:text-white hover:border-slate-700 transition-colors"
                >
                  <X className="w-3.5 h-3.5" />
                  <span>{t("mail.backToList")}</span>
                </button>

                <div className="flex items-center gap-2 text-xs">
                  <button
                    type="button"
                    onClick={() => {
                      const nowSeen = !(selectedRow?.seen ?? true);
                      void setSeen(selected.uid, nowSeen, { persist: true });
                    }}
                    className="flex items-center gap-1.5 rounded-lg border border-slate-800 bg-slate-900/70 px-2.5 py-1.5 text-slate-300 hover:text-white hover:border-slate-700 transition-colors"
                  >
                    <MailOpen className="w-3.5 h-3.5" />
                    <span className="hidden sm:inline">
                      {selectedRow?.seen === false ? t("mail.markRead") : t("mail.markUnread")}
                    </span>
                  </button>
                  <Link
                    href={`/user/compose?replyTo=${encodeURIComponent(selected.from)}&subject=${encodeURIComponent(selected.subject)}`}
                  >
                    <Button variant="primary" size="sm">
                      <Reply className="w-3.5 h-3.5" />
                      <span>{t("mail.reply")}</span>
                    </Button>
                  </Link>
                  {/* Forward carries the subject but no recipient: it used to
                      prefill the original sender, which sends the message
                      straight back to whoever wrote it. */}
                  <Link href={`/user/compose?subject=${encodeURIComponent(selected.subject)}`}>
                    <Button variant="secondary" size="sm">
                      <Forward className="w-3.5 h-3.5" />
                      <span className="hidden sm:inline">{t("mail.forward")}</span>
                    </Button>
                  </Link>
                </div>
              </div>

              <header className="space-y-4">
                <h1 className="text-lg md:text-xl font-bold text-white tracking-tight leading-snug break-words">
                  {selected.subject || t("mail.noSubject")}
                </h1>

                <div className="flex items-start gap-3">
                  <div className="w-9 h-9 rounded-full bg-gradient-to-tr from-indigo-500/30 to-cyan-500/20 border border-indigo-500/30 text-indigo-200 flex items-center justify-center text-[11px] font-bold flex-shrink-0">
                    {initials(selectedRow?.from_display_name ?? "", selected.from)}
                  </div>
                  <div className="min-w-0 flex-1 text-xs">
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <span className="font-semibold text-slate-100 break-all">{selected.from}</span>
                      <span className="text-[11px] text-slate-500 font-mono">
                        {selected.date
                          ? new Date(selected.date).toLocaleString(locale)
                          : selectedRow
                            ? new Date(selectedRow.received_at).toLocaleString(locale)
                            : ""}
                      </span>
                    </div>
                    {/* Recipients are labelled with translated headings; they
                        used to be introduced by a Portuguese word regardless of
                        the interface language. */}
                    {selected.to.length > 0 && (
                      <div className="text-[11px] text-slate-400 break-all">
                        <span className="text-slate-500">{t("ui.toRecipients")}: </span>
                        {selected.to.join(", ")}
                      </div>
                    )}
                    {selected.cc.length > 0 && (
                      <div className="text-[11px] text-slate-400 break-all">
                        <span className="text-slate-500">{t("ui.ccRecipients")}: </span>
                        {selected.cc.join(", ")}
                      </div>
                    )}
                    {selectedRow && selectedRow.size_bytes > 0 && (
                      <div className="text-[11px] text-slate-500 font-mono">
                        {formatBytes(selectedRow.size_bytes)}
                      </div>
                    )}
                  </div>
                </div>

                {/* Attachment Download Chips */}
                {selected.attachments.length > 0 && (
                  <div className="flex flex-wrap gap-2 text-xs">
                    {selected.attachments.map((filename, idx) => (
                      <a
                        key={`${filename}-${idx}`}
                        href={`/api/v1/mail/message/${selected.uid}/attachment/${encodeURIComponent(filename)}?folder=${encodeURIComponent(folder)}`}
                        download={filename}
                        className="group max-w-full px-3 py-2 rounded-xl bg-slate-900/80 border border-slate-800 hover:border-indigo-500/60 hover:bg-slate-900 text-slate-200 flex items-center gap-2 transition-all"
                      >
                        <Paperclip className="w-3.5 h-3.5 text-indigo-400 flex-shrink-0" />
                        <span className="truncate font-mono text-[11px]">{filename}</span>
                        <Download className="w-3.5 h-3.5 text-slate-500 group-hover:text-indigo-300 flex-shrink-0" />
                      </a>
                    ))}
                  </div>
                )}
              </header>

              {/* Remote content banner, shown only when something was actually
                  held back rather than over every message. */}
              {hasBlockedContent && (
                <div className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/25 flex flex-wrap items-center justify-between gap-2 text-xs text-amber-200">
                  <span className="flex items-center gap-2">
                    <ImageIcon className="w-4 h-4 text-amber-400 flex-shrink-0" />
                    <span>{t("mail.remoteContentBlocked")}</span>
                  </span>
                  <button
                    type="button"
                    onClick={() => setLoadRemoteImages(!loadRemoteImages)}
                    className="px-3 py-1 rounded-lg bg-amber-500/20 hover:bg-amber-500/30 text-amber-100 font-medium transition-colors"
                  >
                    {loadRemoteImages ? t("mail.imagesEnabled") : t("mail.loadImages")}
                  </button>
                </div>
              )}

              {/* Message Body */}
              {readerDocument ? (
                <div className="bg-white rounded-2xl overflow-hidden border border-slate-800 shadow-xl">
                  <iframe
                    title={t("ui.messageBody")}
                    srcDoc={readerDocument}
                    /* allow-scripts, and deliberately not allow-same-origin: the
                       frame stays on an opaque origin - it cannot read a cookie
                       or touch this document - while still running the few lines
                       that report its height and open links outward. Message
                       markup is sanitised before it gets here. */
                    sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
                    referrerPolicy="no-referrer"
                    style={{ height: `${readerHeight}px` }}
                    className="w-full border-0 block"
                  />
                </div>
              ) : (
                <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5">
                  <pre className="whitespace-pre-wrap break-words text-sm text-slate-200 font-sans leading-relaxed">
                    {selected.text || t("mail.emptyBody")}
                  </pre>
                </div>
              )}
            </motion.article>
          ) : (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="flex-1 flex flex-col items-center justify-center text-slate-500 text-sm gap-3 py-20 px-6 text-center"
            >
              <Mail className="w-12 h-12 text-slate-700 stroke-1" />
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

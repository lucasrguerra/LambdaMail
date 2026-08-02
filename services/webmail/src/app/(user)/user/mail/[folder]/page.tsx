"use client";

import React, { use, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { motion, AnimatePresence } from "framer-motion";
import {
  Search,
  Paperclip,
  Reply,
  Forward,
  Inbox,
  Filter,
  Image as ImageIcon,
  Mail,
  Clock,
  User,
  ExternalLink,
} from "lucide-react";
import { useTranslations } from "../../../../../i18n/provider";
import { useMailEvents } from "../../../../../lib/useMailEvents";
import { sanitizeEmailHtml, blockRemoteImages, unblockRemoteImages } from "../../../../../lib/sanitizer";
import { Badge } from "../../../../../components/ui/Badge";
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
}

export default function MailFolderPage({ params }: { params: Promise<{ folder: string }> }) {
  const { folder } = use(params);
  const t = useTranslations();

  const [messages, setMessages] = useState<MessageSummary[]>([]);
  const [selected, setSelected] = useState<RenderedMessage | null>(null);
  const [loadRemoteImages, setLoadRemoteImages] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [activeFilter, setActiveFilter] = useState<string | null>(null);
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
    [folder, t]
  );

  useEffect(() => {
    void loadMessages("");
  }, [loadMessages]);

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

  useEffect(() => {
    const handle = setTimeout(() => void loadMessages(searchQuery), 250);
    return () => clearTimeout(handle);
  }, [searchQuery, loadMessages]);

  const openMessage = async (uid: number) => {
    setLoadRemoteImages(false);
    try {
      const res = await fetch(`/api/v1/mail/message/${uid}?folder=${encodeURIComponent(folder)}`);
      if (!res.ok) throw new Error(t("errors.serverError"));
      const data = await res.json();
      setSelected(data);
      setMessages((current) => current.map((m) => (m.uid === uid ? { ...m, seen: true } : m)));
    } catch (err) {
      setError(err instanceof Error ? err.message : t("errors.serverError"));
    }
  };

  const filteredMessages = messages.filter((msg) => {
    if (activeFilter === "unread") return !msg.seen;
    if (activeFilter === "attachment") return msg.has_attachments;
    return true;
  });

  const sanitizedHtml = selected?.html ? sanitizeEmailHtml(selected.html) : null;
  const bodyHtml = sanitizedHtml
    ? loadRemoteImages
      ? unblockRemoteImages(sanitizedHtml)
      : blockRemoteImages(sanitizedHtml)
    : null;

  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Messages List Column */}
      <div
        className={`${selected ? "hidden md:flex" : "flex"} w-full md:w-96 border-r border-slate-800/80 flex-col bg-slate-900/40 backdrop-blur-md md:flex-shrink-0`}
      >
        {/* Search Bar & Filter Chips */}
        <div className="p-3.5 border-b border-slate-800 space-y-2.5">
          <div className="relative">
            <Search className="w-4 h-4 absolute left-3 top-3 text-slate-400" />
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder={`${t("common.search")} (from:, subject:)...`}
              className="w-full pl-9 pr-3 py-2 text-xs rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
            />
          </div>

          <div className="flex items-center gap-1.5 text-[11px] overflow-x-auto pb-0.5">
            <button
              onClick={() => setActiveFilter(null)}
              className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
                activeFilter === null
                  ? "bg-indigo-600 text-white shadow-sm"
                  : "bg-slate-800/60 text-slate-400 hover:text-slate-200"
              }`}
            >
              {t("mail.allMessages")} ({messages.length})
            </button>
            <button
              onClick={() => setActiveFilter("unread")}
              className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
                activeFilter === "unread"
                  ? "bg-indigo-600 text-white shadow-sm"
                  : "bg-slate-800/60 text-slate-400 hover:text-slate-200"
              }`}
            >
              {t("mail.unreadFilter")}
            </button>
            <button
              onClick={() => setActiveFilter("attachment")}
              className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
                activeFilter === "attachment"
                  ? "bg-indigo-600 text-white shadow-sm"
                  : "bg-slate-800/60 text-slate-400 hover:text-slate-200"
              }`}
            >
              {t("mail.attachmentsFilter")}
            </button>
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
            <div className="p-8 text-center text-xs text-slate-500 italic flex flex-col items-center gap-2">
              <Inbox className="w-8 h-8 text-slate-600" />
              <span>{t("mail.noMessages")}</span>
            </div>
          ) : (
            filteredMessages.map((msg) => {
              const isSelected = selected?.uid === msg.uid;
              return (
                <button
                  key={msg.uid}
                  onClick={() => void openMessage(msg.uid)}
                  className={`w-full text-left p-4 transition-all duration-150 block relative ${
                    isSelected
                      ? "bg-indigo-600/15 border-l-4 border-indigo-500"
                      : "hover:bg-slate-800/30"
                  }`}
                >
                  <div className="flex items-center justify-between mb-1.5">
                    <span
                      className={`text-xs font-semibold truncate max-w-[170px] ${
                        msg.seen ? "text-slate-300" : "text-white font-bold"
                      }`}
                    >
                      {msg.from_display_name || msg.sender_address}
                    </span>
                    <span className="text-[10px] text-slate-500 font-mono">
                      {new Date(msg.received_at).toLocaleTimeString([], {
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </span>
                  </div>
                  <div
                    className={`text-xs truncate mb-1 flex items-center gap-1.5 ${
                      msg.seen ? "text-slate-300" : "font-bold text-slate-100"
                    }`}
                  >
                    {!msg.seen && (
                      <span className="w-2 h-2 rounded-full bg-indigo-500 flex-shrink-0" />
                    )}
                    <span className="truncate">{msg.subject || "(Sem assunto)"}</span>
                    {msg.has_attachments && (
                      <Paperclip className="w-3.5 h-3.5 text-slate-400 flex-shrink-0" />
                    )}
                  </div>
                  <div className="text-[11px] text-slate-400 truncate leading-relaxed">
                    {msg.snippet}
                  </div>
                </button>
              );
            })
          )}
        </div>
      </div>

      {/* Message Reader Column */}
      <div
        className={`${selected ? "flex" : "hidden md:flex"} flex-1 flex-col bg-dark-bg overflow-y-auto p-4 sm:p-6 md:p-8`}
      >
        {selected && (
          <button
            type="button"
            onClick={() => setSelected(null)}
            className="md:hidden mb-4 self-start rounded-lg border border-slate-700 px-3 py-1.5 text-xs text-slate-300"
          >
            &larr; {t("mail.inbox")}
          </button>
        )}
        <AnimatePresence mode="wait">
          {selected ? (
            <motion.div
              key={selected.uid}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -10 }}
              transition={{ duration: 0.2 }}
              className="max-w-4xl w-full mx-auto space-y-6"
            >
              {/* Header & Reply/Forward Actions */}
              <div className="glass-panel p-6 rounded-2xl border border-slate-800 space-y-4">
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-slate-800/80 pb-4">
                  <h1 className="text-xl md:text-2xl font-extrabold text-white tracking-tight">
                    {selected.subject || "(Sem assunto)"}
                  </h1>

                  {/* Action Buttons */}
                  <div className="flex items-center gap-2 text-xs">
                    <Link
                      href={`/user/compose?replyTo=${encodeURIComponent(
                        selected.from
                      )}&subject=${encodeURIComponent(selected.subject)}`}
                    >
                      <Button variant="primary" size="sm">
                        <Reply className="w-3.5 h-3.5" />
                        <span>{t("mail.reply")}</span>
                      </Button>
                    </Link>

                    <Link
                      href={`/user/compose?replyTo=${encodeURIComponent(
                        selected.from
                      )}&subject=${encodeURIComponent(selected.subject)}`}
                    >
                      <Button variant="secondary" size="sm">
                        <Forward className="w-3.5 h-3.5" />
                        <span>{t("mail.forward")}</span>
                      </Button>
                    </Link>
                  </div>
                </div>

                <div className="flex items-center justify-between text-xs text-slate-400">
                  <div className="flex items-center gap-2">
                    <div className="w-8 h-8 rounded-full bg-indigo-500/20 border border-indigo-500/30 text-indigo-400 flex items-center justify-center font-bold">
                      <User className="w-4 h-4" />
                    </div>
                    <div>
                      <div className="font-semibold text-slate-100 text-xs">{selected.from}</div>
                      {selected.to.length > 0 && (
                        <div className="text-[10px] text-slate-400">
                          Para: {selected.to.join(", ")}
                        </div>
                      )}
                    </div>
                  </div>
                  <div className="font-mono text-slate-400 text-[11px]">
                    {selected.date ? new Date(selected.date).toLocaleString() : ""}
                  </div>
                </div>

                {/* Attachment Download Chips */}
                {selected.attachments.length > 0 && (
                  <div className="pt-3 border-t border-slate-800/80 flex flex-wrap gap-2 text-xs">
                    {selected.attachments.map((filename, idx) => (
                      <a
                        key={idx}
                        href={`/api/v1/mail/message/${selected.uid}/attachment/${encodeURIComponent(
                          filename
                        )}`}
                        download={filename}
                        className="px-3 py-1.5 rounded-xl bg-slate-900/90 border border-slate-800 hover:border-indigo-500 text-indigo-300 text-xs font-mono flex items-center gap-2 transition-all shadow-sm"
                      >
                        <Paperclip className="w-3.5 h-3.5 text-indigo-400" />
                        <span>{filename}</span>
                        <ExternalLink className="w-3 h-3 text-slate-500" />
                      </a>
                    ))}
                  </div>
                )}
              </div>

              {/* Remote Images Privacy Banner */}
              {selected.html && (
                <div className="p-3.5 rounded-xl bg-amber-500/10 border border-amber-500/25 flex items-center justify-between text-xs text-amber-300">
                  <div className="flex items-center gap-2">
                    <ImageIcon className="w-4 h-4 text-amber-400" />
                    <span>{t("mail.loadImages")} (sanitised)</span>
                  </div>
                  <button
                    onClick={() => setLoadRemoteImages(!loadRemoteImages)}
                    className="px-3 py-1 rounded-lg bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 font-medium transition-colors"
                  >
                    {loadRemoteImages ? "Imagens Habilitadas" : t("mail.loadImages")}
                  </button>
                </div>
              )}

              {/* Message Body Container */}
              {bodyHtml ? (
                <div className="bg-white rounded-2xl overflow-hidden shadow-2xl border border-slate-800 min-h-[350px]">
                  <iframe
                    title={t("ui.messageBody")}
                    srcDoc={bodyHtml}
                    sandbox=""
                    className="w-full h-[500px] border-0"
                  />
                </div>
              ) : (
                <div className="glass-panel p-6 rounded-2xl border border-slate-800">
                  <pre className="whitespace-pre-wrap break-words text-sm text-slate-200 font-sans leading-relaxed">
                    {selected.text}
                  </pre>
                </div>
              )}
            </motion.div>
          ) : (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="flex-1 flex flex-col items-center justify-center text-slate-500 text-sm gap-3 py-20"
            >
              <Mail className="w-12 h-12 text-slate-700 stroke-1" />
              <span>{t("mail.noMessages")}</span>
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </div>
  );
}

"use client";

import React, { useCallback, useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";

/**
 * The outbound queue, read from the database.
 *
 * This screen used to be a fixture: two invented jobs ("job-101",
 * user@example.com -> partner@remotehost.com) held in component state, whose
 * Retry and Freeze buttons only edited that local array. It showed the same two
 * rows on every deployment, never displayed a real stuck message, and reported
 * success for actions that had not happened. /api/v1/admin/queue was already
 * serving the real thing.
 */

/** Mirrors the outbound_jobs row shape returned by the queue endpoint. */
interface QueueJob {
  id: string;
  envelope_from: string;
  envelope_to: string;
  destination_domain: string;
  status: string;
  attempt: number;
  next_attempt_at: string | null;
  last_smtp_code: number | null;
  last_error: string | null;
}

interface QueueSummary {
  by_status: Record<string, number>;
  recent: QueueJob[];
}

/** Only failure states earn a warning colour; the rest are ordinary progress. */
function statusClass(status: string): string {
  if (status === "BOUNCED" || status === "FROZEN") return "badge-danger";
  if (status === "DEFERRED") return "badge-warning";
  return "badge-verified";
}

export default function AdminQueuePage() {
  const t = useTranslations();
  const [summary, setSummary] = useState<QueueSummary | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v1/admin/queue");
      if (!res.ok) throw new Error();
      setSummary(await res.json());
      setError(null);
    } catch {
      // Left null so the table renders empty rather than showing stale or
      // invented rows while the service is unreachable.
      setSummary(null);
      setError(t("errors.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  /**
   * Retry and cancel are the two actions the service actually implements.
   * The old "Freeze" button named a third that has never existed anywhere in
   * the backend, so pressing it changed nothing but the colour of a cell.
   */
  const act = async (id: string, action: "retry" | "cancel") => {
    setMessage(null);
    setError(null);
    const res = await fetch(`/api/v1/admin/queue/${encodeURIComponent(id)}/${action}`, {
      method: "POST",
    });
    if (!res.ok) {
      setError(t("errors.serverError"));
      return;
    }
    setMessage(t(action === "retry" ? "admin.jobRetried" : "admin.jobCancelled", { id }));
    // Re-read rather than patching local state, so what is on screen is what
    // the queue now holds.
    await load();
  };

  const jobs = summary?.recent ?? [];

  return (
    <div className="p-6 md:p-8 space-y-6 max-w-7xl mx-auto">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-white mb-1">{t("admin.queueTitle")}</h1>
          <p className="text-xs text-slate-400">{t("admin.queueSubtitle")}</p>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          disabled={loading}
          className="flex items-center gap-2 self-start rounded-xl border border-slate-700 px-3.5 py-2 text-xs font-medium text-slate-300 transition-colors hover:border-emerald-500/50 hover:text-white disabled:opacity-50"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
          <span>{t("common.refresh")}</span>
        </button>
      </div>

      {/* Counts per status, straight from the by_status aggregate. */}
      {summary && Object.keys(summary.by_status).length > 0 && (
        <div className="flex flex-wrap gap-2">
          {Object.entries(summary.by_status).map(([status, count]) => (
            <span
              key={status}
              className="rounded-lg border border-slate-800 bg-slate-900/70 px-3 py-1.5 text-xs text-slate-300"
            >
              <span className="font-mono font-bold text-white">{count}</span> {status}
            </span>
          ))}
        </div>
      )}

      {message && (
        <div className="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 text-xs">
          {message}
        </div>
      )}
      {error && (
        <div className="p-4 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs">
          {error}
        </div>
      )}

      <div className="glass-panel rounded-2xl border border-slate-800 overflow-hidden">
        <div className="p-4 border-b border-slate-800 bg-slate-900/60 font-bold text-xs text-slate-300">
          {t("admin.queueTitle")} ({jobs.length})
        </div>

        {jobs.length === 0 && !loading ? (
          <div className="p-8 text-center text-xs text-slate-500">{t("admin.queueEmpty")}</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse text-xs">
              <thead>
                <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                  <th className="p-3">{t("ui.jobId")}</th>
                  <th className="p-3">{t("ui.sender")}</th>
                  <th className="p-3">{t("ui.recipient")}</th>
                  <th className="p-3">{t("ui.destination")}</th>
                  <th className="p-3">{t("ui.attempts")}</th>
                  <th className="p-3">{t("ui.lastError")}</th>
                  <th className="p-3">{t("common.status")}</th>
                  <th className="p-3">{t("ui.actions")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/60 font-mono">
                {jobs.map((j) => (
                  <tr key={j.id} className="hover:bg-slate-900/30">
                    <td className="p-3 font-bold text-slate-200">{j.id.slice(0, 8)}</td>
                    <td className="p-3 text-slate-300">{j.envelope_from}</td>
                    <td className="p-3 text-slate-300">{j.envelope_to}</td>
                    <td className="p-3 text-emerald-400">{j.destination_domain}</td>
                    <td className="p-3 text-slate-400">{j.attempt}</td>
                    <td className="p-3 text-slate-400 truncate max-w-xs" title={j.last_error ?? ""}>
                      {j.last_error
                        ? `${j.last_smtp_code ? `${j.last_smtp_code} ` : ""}${j.last_error}`
                        : t("common.none")}
                    </td>
                    <td className="p-3">
                      <span className={`${statusClass(j.status)} px-2 py-0.5 rounded text-[10px]`}>
                        {j.status}
                      </span>
                    </td>
                    <td className="p-3 flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => void act(j.id, "retry")}
                        className="px-2 py-1 rounded bg-indigo-600/20 hover:bg-indigo-600/30 text-indigo-300 text-[10px] font-bold"
                      >
                        {t("admin.retryJob")}
                      </button>
                      <button
                        type="button"
                        onClick={() => void act(j.id, "cancel")}
                        className="px-2 py-1 rounded bg-red-600/20 hover:bg-red-600/30 text-red-300 text-[10px] font-bold"
                      >
                        {t("admin.cancelJob")}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

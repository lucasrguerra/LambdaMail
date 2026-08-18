"use client";

import React, { useCallback, useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";

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

/**
 * Only failure states earn a warning colour; the rest are ordinary progress.
 *
 * These used to return "badge-danger", "badge-warning" and "badge-verified" -
 * three class names no stylesheet in this project ever defined, so every status
 * rendered as unstyled text. They are Badge variants now, which exist.
 */
function statusVariant(status: string): "danger" | "warning" | "neutral" {
  if (status === "BOUNCED" || status === "FROZEN") return "danger";
  if (status === "DEFERRED") return "warning";
  return "neutral";
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
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-[18px] px-5 pb-11 pt-7 sm:px-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("admin.queueTitle")}</h1>
          <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.queueSubtitle")}</p>
        </div>
        <Button variant="secondary" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
          <span>{t("common.refresh")}</span>
        </Button>
      </div>

      {/* Counts per status, straight from the by_status aggregate. */}
      {summary && Object.keys(summary.by_status).length > 0 && (
        <div className="flex flex-wrap gap-2">
          {Object.entries(summary.by_status).map(([status, count]) => (
            <span
              key={status}
              className="flex items-center gap-2.5 rounded-xl bg-dark-panel px-3.5 py-2.5 shadow-edge"
            >
              <span className="text-[19px] font-medium leading-none tabular-nums text-slate-100">{count}</span>
              <span className="text-xs text-slate-400">{status}</span>
            </span>
          ))}
        </div>
      )}

      {message && (
        <div className="rounded-xl bg-dark-card px-4 py-3 text-[12.5px] text-slate-200 shadow-edge">{message}</div>
      )}
      {error && (
        <div className="rounded-xl bg-rose-900/60 px-4 py-3 text-[12.5px] text-rose-200 shadow-edge">{error}</div>
      )}

      <div className="rounded-2xl bg-dark-panel px-[18px] pb-3 pt-1.5 shadow-edge">
        {jobs.length === 0 && !loading ? (
          <div className="p-8 text-center text-[13px] text-slate-400">{t("admin.queueEmpty")}</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="lm-table">
              <thead>
                <tr>
                  <th className="pl-0">{t("ui.jobId")}</th>
                  <th>{t("ui.sender")}</th>
                  <th>{t("ui.recipient")}</th>
                  <th>{t("ui.destination")}</th>
                  <th>{t("ui.attempts")}</th>
                  <th>{t("ui.lastError")}</th>
                  <th>{t("common.status")}</th>
                  <th className="pr-0 text-right">{t("ui.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((j) => (
                  <tr key={j.id}>
                    <td className="pl-0 font-mono text-[12.5px] text-slate-100">{j.id.slice(0, 8)}</td>
                    <td className="max-w-[180px] break-words text-[12.5px] text-slate-300">{j.envelope_from}</td>
                    <td className="max-w-[180px] break-words text-[12.5px] text-slate-300">{j.envelope_to}</td>
                    <td className="break-words text-[12.5px] text-slate-300">{j.destination_domain}</td>
                    <td className="whitespace-nowrap text-[12.5px] tabular-nums text-slate-300">{j.attempt}</td>
                    {/* The diagnostic wraps instead of being truncated: a queue
                        screen exists to show why a message did not leave. */}
                    <td className="max-w-[250px] text-xs leading-relaxed text-slate-400">
                      {j.last_error
                        ? `${j.last_smtp_code ? `${j.last_smtp_code} ` : ""}${j.last_error}`
                        : t("common.none")}
                    </td>
                    <td>
                      <Badge variant={statusVariant(j.status)} className="whitespace-nowrap">
                        {j.status}
                      </Badge>
                    </td>
                    <td className="whitespace-nowrap pr-0 text-right">
                      <Button variant="secondary" size="sm" onClick={() => void act(j.id, "retry")}>
                        {t("admin.retryJob")}
                      </Button>{" "}
                      <Button variant="ghost" size="sm" onClick={() => void act(j.id, "cancel")}>
                        {t("admin.cancelJob")}
                      </Button>
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

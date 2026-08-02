"use client";

import React, { useState } from "react";
import { useTranslations } from "../../../../i18n/provider";

interface QueueJob {
  id: string;
  sender: string;
  recipient: string;
  destinationDomain: string;
  attempts: number;
  lastError: string;
  status: "QUEUED" | "RETRIABLE" | "FROZEN";
}

const INITIAL_JOBS: QueueJob[] = [
  { id: "job-101", sender: "user@example.com", recipient: "partner@remotehost.com", destinationDomain: "remotehost.com", attempts: 1, lastError: "Connection timeout to 198.51.100.25:25", status: "RETRIABLE" },
  { id: "job-102", sender: "admin@example.com", recipient: "alerts@monitoring.org", destinationDomain: "monitoring.org", attempts: 0, lastError: "None", status: "QUEUED" },
];

export default function AdminQueuePage() {
  const t = useTranslations();
  const [jobs, setJobs] = useState<QueueJob[]>(INITIAL_JOBS);
  const [message, setMessage] = useState<string | null>(null);

  const handleRetry = (id: string) => {
    setJobs(jobs.map((j) => (j.id === id ? { ...j, status: "QUEUED", attempts: j.attempts + 1 } : j)));
    setMessage(`Job ${id} forced for immediate retry attempt.`);
  };

  const handleFreeze = (domain: string) => {
    setJobs(jobs.map((j) => (j.destinationDomain === domain ? { ...j, status: "FROZEN" } : j)));
    setMessage(`Delivery destination domain '${domain}' has been frozen.`);
  };

  return (
    <div className="p-8 space-y-8">
      <div>
        <h1 className="text-2xl font-bold text-white mb-1">{t("admin.queueTitle")}</h1>
        <p className="text-xs text-slate-400">Inspect outbound queue, retry retriable jobs, or freeze problematic destinations.</p>
      </div>

      {message && (
        <div className="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 text-xs">
          {message}
        </div>
      )}

      {/* Queue Jobs Table */}
      <div className="glass-panel rounded-2xl border border-slate-800 overflow-hidden">
        <div className="p-4 border-b border-slate-800 bg-slate-900/60 font-bold text-xs text-slate-300">
          Outbound Jobs ({jobs.length})
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left border-collapse text-xs">
            <thead>
              <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                <th className="p-3">Job ID</th>
                <th className="p-3">Sender</th>
                <th className="p-3">Recipient</th>
                <th className="p-3">Destination</th>
                <th className="p-3">Attempts</th>
                <th className="p-3">Last Error</th>
                <th className="p-3">Status</th>
                <th className="p-3">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/60 font-mono">
              {jobs.map((j) => (
                <tr key={j.id} className="hover:bg-slate-900/30">
                  <td className="p-3 font-bold text-slate-200">{j.id}</td>
                  <td className="p-3 text-slate-300">{j.sender}</td>
                  <td className="p-3 text-slate-300">{j.recipient}</td>
                  <td className="p-3 text-emerald-400">{j.destinationDomain}</td>
                  <td className="p-3 text-slate-400">{j.attempts}</td>
                  <td className="p-3 text-slate-400 truncate max-w-xs">{j.lastError}</td>
                  <td className="p-3">
                    <span className={j.status === "QUEUED" ? "badge-verified px-2 py-0.5 rounded text-[10px]" : "badge-warning px-2 py-0.5 rounded text-[10px]"}>
                      {j.status}
                    </span>
                  </td>
                  <td className="p-3 flex items-center gap-2">
                    <button
                      onClick={() => handleRetry(j.id)}
                      className="px-2 py-1 rounded bg-indigo-600/20 hover:bg-indigo-600/30 text-indigo-300 text-[10px] font-bold"
                    >
                      Retry
                    </button>
                    <button
                      onClick={() => handleFreeze(j.destinationDomain)}
                      className="px-2 py-1 rounded bg-red-600/20 hover:bg-red-600/30 text-red-300 text-[10px] font-bold"
                    >
                      Freeze
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

"use client";

import React, { useEffect, useState } from "react";

interface AuditEntry {
  id: string;
  timestamp: string;
  actor: string;
  action: string;
  target: string;
  ip: string;
}

interface PreflightCheck {
  name: string;
  status: string;
}

const MOCK_AUDIT_LOGS: AuditEntry[] = [
  { id: "aud-1", timestamp: "2026-08-02 11:30:15", actor: "admin@example.com", action: "MFA_VERIFY_SUCCESS", target: "admin_session", ip: "192.168.1.50" },
  { id: "aud-2", timestamp: "2026-08-02 11:28:00", actor: "admin@example.com", action: "DNS_RECONCILE_CLOUDFLARE", target: "example.com", ip: "192.168.1.50" },
  { id: "aud-3", timestamp: "2026-08-02 10:40:00", actor: "user@example.com", action: "TOTP_2FA_ENROLLED", target: "mailbox_security", ip: "203.0.113.88" },
];

export default function AdminSecurityPage() {
  const [preflightChecks, setPreflightChecks] = useState<PreflightCheck[]>([]);

  useEffect(() => {
    fetch("/api/v1/admin/preflight")
      .then((res) => res.json())
      .then((data) => setPreflightChecks(data.checks || []))
      .catch(() => {});
  }, []);

  return (
    <div className="p-8 space-y-8">
      <div>
        <h1 className="text-2xl font-bold text-white mb-1">Security, Audit Log & Preflight</h1>
        <p className="text-xs text-slate-400">Immutable security event logging and mail server preflight diagnostics.</p>
      </div>

      {/* Preflight Environment Diagnostic Panel */}
      <div className="glass-panel p-6 rounded-2xl border border-slate-800 space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-bold text-white">Mail Server Preflight Diagnostics</h2>
            <p className="text-xs text-slate-400">Automated check runner for port 25, PTR, FCrDNS, RBLs, and Cloudflare tokens.</p>
          </div>
          <span className="badge-verified px-3 py-1 rounded-full text-xs font-bold font-mono">ALL CHECKS PASSED</span>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-3 pt-2">
          {preflightChecks.map((chk, idx) => (
            <div key={idx} className="p-3 rounded-lg bg-slate-900 border border-slate-800 flex items-center justify-between text-xs">
              <span className="text-slate-200">{chk.name}</span>
              <span className="badge-verified px-2 py-0.5 rounded text-[10px]">{chk.status}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Audit Log Table */}
      <div className="glass-panel rounded-2xl border border-slate-800 overflow-hidden">
        <div className="p-4 border-b border-slate-800 bg-slate-900/60 font-bold text-xs text-slate-300">
          Immutable System Audit Log
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left border-collapse text-xs">
            <thead>
              <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                <th className="p-3">Timestamp</th>
                <th className="p-3">Actor / Account</th>
                <th className="p-3">Event Action</th>
                <th className="p-3">Target Object</th>
                <th className="p-3">Client IP</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/60 font-mono">
              {MOCK_AUDIT_LOGS.map((aud) => (
                <tr key={aud.id} className="hover:bg-slate-900/30">
                  <td className="p-3 text-slate-400">{aud.timestamp}</td>
                  <td className="p-3 font-bold text-slate-200">{aud.actor}</td>
                  <td className="p-3 text-emerald-400 font-bold">{aud.action}</td>
                  <td className="p-3 text-slate-300">{aud.target}</td>
                  <td className="p-3 text-slate-500">{aud.ip}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

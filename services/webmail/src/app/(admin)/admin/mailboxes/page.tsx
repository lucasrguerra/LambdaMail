"use client";

import React, { useState } from "react";

interface Mailbox {
  id: string;
  email: string;
  role: string;
  storageUsedMb: number;
  quotaMb: number;
  mfaEnabled: boolean;
  locked: boolean;
}

const INITIAL_MAILBOXES: Mailbox[] = [
  { id: "mb-1", email: "admin@example.com", role: "SUPER_ADMIN", storageUsedMb: 120, quotaMb: 5000, mfaEnabled: true, locked: false },
  { id: "mb-2", email: "user@example.com", role: "USER", storageUsedMb: 450, quotaMb: 2000, mfaEnabled: false, locked: false },
  { id: "mb-3", email: "postmaster@example.com", role: "DOMAIN_ADMIN", storageUsedMb: 15, quotaMb: 1000, mfaEnabled: true, locked: false },
];

export default function AdminMailboxesPage() {
  const [mailboxes, setMailboxes] = useState<Mailbox[]>(INITIAL_MAILBOXES);
  const [mfaPolicy, setMfaPolicy] = useState<"optional" | "required_admins" | "required_all">("required_admins");
  const [newEmail, setNewEmail] = useState("");
  const [newRole, setNewRole] = useState("USER");

  const handleCreateMailbox = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newEmail) return;
    const newMb: Mailbox = {
      id: `mb-${Date.now()}`,
      email: newEmail,
      role: newRole,
      storageUsedMb: 0,
      quotaMb: 2000,
      mfaEnabled: false,
      locked: false,
    };
    setMailboxes([...mailboxes, newMb]);
    setNewEmail("");
  };

  return (
    <div className="p-8 space-y-8">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white mb-1">Mailboxes & Account Management</h1>
          <p className="text-xs text-slate-400">Configure mailboxes, quota allocation, and domain MFA policy.</p>
        </div>
      </div>

      {/* Domain MFA Enforcement Policy Controls */}
      <div className="glass-panel p-6 rounded-2xl border border-slate-800 flex items-center justify-between">
        <div>
          <h2 className="text-sm font-bold text-white">Domain MFA Enforcement Policy (domains.mfa_policy)</h2>
          <p className="text-xs text-slate-400">Enforce TOTP 2FA requirements across all domain accounts.</p>
        </div>

        <div className="flex gap-2 text-xs">
          <button
            onClick={() => setMfaPolicy("optional")}
            className={`px-3 py-1.5 rounded-lg border font-medium transition-colors ${
              mfaPolicy === "optional" ? "bg-slate-800 border-slate-600 text-white" : "border-slate-800 text-slate-400"
            }`}
          >
            Optional
          </button>
          <button
            onClick={() => setMfaPolicy("required_admins")}
            className={`px-3 py-1.5 rounded-lg border font-medium transition-colors ${
              mfaPolicy === "required_admins" ? "bg-emerald-600/20 border-emerald-500 text-emerald-300" : "border-slate-800 text-slate-400"
            }`}
          >
            Required for Admins
          </button>
          <button
            onClick={() => setMfaPolicy("required_all")}
            className={`px-3 py-1.5 rounded-lg border font-medium transition-colors ${
              mfaPolicy === "required_all" ? "bg-indigo-600/20 border-indigo-500 text-indigo-300" : "border-slate-800 text-slate-400"
            }`}
          >
            Required for All
          </button>
        </div>
      </div>

      {/* Create Mailbox Form */}
      <div className="glass-panel p-6 rounded-2xl border border-slate-800">
        <h2 className="text-sm font-bold text-white mb-4">Provision New Mailbox Account</h2>
        <form onSubmit={handleCreateMailbox} className="flex gap-3">
          <input
            type="email"
            value={newEmail}
            onChange={(e) => setNewEmail(e.target.value)}
            placeholder="newaccount@example.com"
            required
            className="flex-1 px-4 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
          />
          <select
            value={newRole}
            onChange={(e) => setNewRole(e.target.value)}
            className="px-4 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-emerald-500"
          >
            <option value="USER">USER</option>
            <option value="DOMAIN_ADMIN">DOMAIN_ADMIN</option>
            <option value="SUPER_ADMIN">SUPER_ADMIN</option>
          </select>
          <button
            type="submit"
            className="py-2 px-5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-xs transition-colors"
          >
            Create Account
          </button>
        </form>
      </div>

      {/* Mailboxes Table */}
      <div className="glass-panel rounded-2xl border border-slate-800 overflow-hidden">
        <div className="p-4 border-b border-slate-800 bg-slate-900/60 font-bold text-xs text-slate-300">
          Provisioned Mailbox Accounts ({mailboxes.length})
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left border-collapse text-xs">
            <thead>
              <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                <th className="p-3">Email Address</th>
                <th className="p-3">Role</th>
                <th className="p-3">Storage Quota</th>
                <th className="p-3">2FA Status</th>
                <th className="p-3">Account Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/60 font-mono">
              {mailboxes.map((mb) => (
                <tr key={mb.id} className="hover:bg-slate-900/30">
                  <td className="p-3 font-bold text-slate-100">{mb.email}</td>
                  <td className="p-3 text-slate-300">{mb.role}</td>
                  <td className="p-3">
                    <div className="flex items-center gap-2">
                      <div className="w-24 h-2 bg-slate-800 rounded-full overflow-hidden">
                        <div
                          className="h-full bg-emerald-500 rounded-full"
                          style={{ width: `${(mb.storageUsedMb / mb.quotaMb) * 100}%` }}
                        />
                      </div>
                      <span className="text-[10px] text-slate-400">{mb.storageUsedMb}MB / {mb.quotaMb}MB</span>
                    </div>
                  </td>
                  <td className="p-3">
                    <span className={mb.mfaEnabled ? "badge-verified px-2 py-0.5 rounded text-[10px]" : "badge-warning px-2 py-0.5 rounded text-[10px]"}>
                      {mb.mfaEnabled ? "TOTP ACTIVE" : "2FA INACTIVE"}
                    </span>
                  </td>
                  <td className="p-3">
                    <span className="badge-verified px-2 py-0.5 rounded text-[10px]">ACTIVE</span>
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

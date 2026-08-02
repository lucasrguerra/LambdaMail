"use client";

import React, { useState } from "react";
import { useTranslations } from "../../../../i18n/provider";

interface Mailbox {
  id: string;
  email: string;
  role: string;
  storageUsedMb: number;
  quotaMb: number;
  mfaEnabled: boolean;
  locked: boolean;
}

interface Alias {
  id: string;
  aliasAddress: string;
  targetAddress: string;
  domain: string;
}

const INITIAL_MAILBOXES: Mailbox[] = [
  { id: "mb-1", email: "admin@example.com", role: "SUPER_ADMIN", storageUsedMb: 120, quotaMb: 5000, mfaEnabled: true, locked: false },
  { id: "mb-2", email: "user@example.com", role: "USER", storageUsedMb: 450, quotaMb: 2000, mfaEnabled: false, locked: false },
  { id: "mb-3", email: "postmaster@example.com", role: "DOMAIN_ADMIN", storageUsedMb: 15, quotaMb: 1000, mfaEnabled: true, locked: false },
];

const INITIAL_ALIASES: Alias[] = [
  { id: "al-1", aliasAddress: "support@example.com", targetAddress: "user@example.com", domain: "example.com" },
  { id: "al-2", aliasAddress: "abuse@example.com", targetAddress: "admin@example.com", domain: "example.com" },
];

export default function AdminMailboxesPage() {
  const t = useTranslations();

  const [activeTab, setActiveTab] = useState<"mailboxes" | "aliases" | "csv">("mailboxes");
  const [mailboxes, setMailboxes] = useState<Mailbox[]>(INITIAL_MAILBOXES);
  const [aliases, setAliases] = useState<Alias[]>(INITIAL_ALIASES);
  const [mfaPolicy, setMfaPolicy] = useState<"optional" | "required_admins" | "required_all">("required_admins");

  // Create mailbox state
  const [newEmail, setNewEmail] = useState("");
  const [newRole, setNewRole] = useState("USER");
  const [newQuota, setNewQuota] = useState(2000);

  // Create alias state
  const [newAliasAddr, setNewAliasAddr] = useState("");
  const [newTargetAddr, setNewTargetAddr] = useState("");

  // CSV Import state
  const [csvText, setCsvText] = useState("");
  const [csvPreview, setCsvPreview] = useState<{ email: string; role: string; quotaMb: number }[]>([]);
  const [csvSuccessMessage, setCsvSuccessMessage] = useState<string | null>(null);

  const handleCreateMailbox = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newEmail) return;
    const newMb: Mailbox = {
      id: `mb-${Date.now()}`,
      email: newEmail,
      role: newRole,
      storageUsedMb: 0,
      quotaMb: newQuota,
      mfaEnabled: false,
      locked: false,
    };
    setMailboxes([...mailboxes, newMb]);
    setNewEmail("");
  };

  const handleToggleLock = (id: string) => {
    setMailboxes(mailboxes.map((mb) => (mb.id === id ? { ...mb, locked: !mb.locked } : mb)));
  };

  const handleDeleteMailbox = (id: string) => {
    setMailboxes(mailboxes.filter((mb) => mb.id !== id));
  };

  const handleCreateAlias = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newAliasAddr || !newTargetAddr) return;
    const domain = newAliasAddr.split("@")[1] || "example.com";
    setAliases([...aliases, { id: `al-${Date.now()}`, aliasAddress: newAliasAddr, targetAddress: newTargetAddr, domain }]);
    setNewAliasAddr("");
    setNewTargetAddr("");
  };

  const handleDeleteAlias = (id: string) => {
    setAliases(aliases.filter((al) => al.id !== id));
  };

  const handleParseCsv = () => {
    const lines = csvText.split("\n").map((l) => l.trim()).filter(Boolean);
    const parsed: { email: string; role: string; quotaMb: number }[] = [];
    for (const line of lines) {
      if (line.startsWith("email") || line.startsWith("#")) continue; // skip header
      const parts = line.split(",").map((p) => p.trim());
      if (parts.length >= 1 && parts[0].includes("@")) {
        parsed.push({
          email: parts[0],
          role: parts[1] || "USER",
          quotaMb: parseInt(parts[2] || "2000", 10),
        });
      }
    }
    setCsvPreview(parsed);
  };

  const handleExecuteCsvImport = () => {
    const imported: Mailbox[] = csvPreview.map((item, idx) => ({
      id: `csv-${Date.now()}-${idx}`,
      email: item.email,
      role: item.role,
      storageUsedMb: 0,
      quotaMb: item.quotaMb,
      mfaEnabled: false,
      locked: false,
    }));
    setMailboxes((current) => [...current, ...imported]);
    setCsvSuccessMessage(`Successfully imported ${imported.length} mailboxes!`);
    setCsvText("");
    setCsvPreview([]);
    setTimeout(() => setCsvSuccessMessage(null), 3000);
  };

  return (
    <div className="p-8 space-y-8">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white mb-1">{t("admin.mailboxesTitle")}</h1>
          <p className="text-xs text-slate-400">Configure mailboxes, aliases, bulk CSV import, and MFA policies.</p>
        </div>

        {/* Tab Navigation */}
        <div className="flex bg-slate-900 p-1 rounded-xl border border-slate-800 text-xs font-medium">
          <button
            onClick={() => setActiveTab("mailboxes")}
            className={`px-4 py-2 rounded-lg transition-colors ${
              activeTab === "mailboxes" ? "bg-emerald-600 text-white" : "text-slate-400 hover:text-slate-200"
            }`}
          >
            Mailboxes ({mailboxes.length})
          </button>
          <button
            onClick={() => setActiveTab("aliases")}
            className={`px-4 py-2 rounded-lg transition-colors ${
              activeTab === "aliases" ? "bg-emerald-600 text-white" : "text-slate-400 hover:text-slate-200"
            }`}
          >
            Aliases ({aliases.length})
          </button>
          <button
            onClick={() => setActiveTab("csv")}
            className={`px-4 py-2 rounded-lg transition-colors ${
              activeTab === "csv" ? "bg-emerald-600 text-white" : "text-slate-400 hover:text-slate-200"
            }`}
          >
            CSV Bulk Import
          </button>
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

      {/* TAB 1: MAILBOXES */}
      {activeTab === "mailboxes" && (
        <div className="space-y-6">
          <div className="glass-panel p-6 rounded-2xl border border-slate-800">
            <h2 className="text-sm font-bold text-white mb-4">{t("ui.provisionMailbox")}</h2>
            <form onSubmit={handleCreateMailbox} className="flex gap-3 text-xs">
              <input
                type="email"
                value={newEmail}
                onChange={(e) => setNewEmail(e.target.value)}
                placeholder="newaccount@example.com"
                required
                className="flex-1 px-4 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <select
                value={newRole}
                onChange={(e) => setNewRole(e.target.value)}
                className="px-4 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-emerald-500"
              >
                <option value="USER">USER</option>
                <option value="DOMAIN_ADMIN">DOMAIN_ADMIN</option>
                <option value="SUPER_ADMIN">SUPER_ADMIN</option>
              </select>
              <input
                type="number"
                value={newQuota}
                onChange={(e) => setNewQuota(parseInt(e.target.value, 10))}
                placeholder="Quota (MB)"
                className="w-32 px-4 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-emerald-500"
              />
              <button
                type="submit"
                className="py-2 px-5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium transition-colors"
              >
                Create Account
              </button>
            </form>
          </div>

          <div className="glass-panel rounded-2xl border border-slate-800 overflow-hidden">
            <div className="p-4 border-b border-slate-800 bg-slate-900/60 font-bold text-xs text-slate-300">
              Provisioned Mailbox Accounts ({mailboxes.length})
            </div>

            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                    <th className="p-3">{t("ui.emailAddress")}</th>
                    <th className="p-3">Role</th>
                    <th className="p-3">{t("ui.storageQuota")}</th>
                    <th className="p-3">2FA Status</th>
                    <th className="p-3">{t("ui.accountStatus")}</th>
                    <th className="p-3">Actions</th>
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
                        <span className={`px-2 py-0.5 rounded text-[10px] ${mb.locked ? "bg-red-500/20 text-red-300 border border-red-500/40" : "badge-verified"}`}>
                          {mb.locked ? "LOCKED" : t("ui.active")}
                        </span>
                      </td>
                      <td className="p-3 flex items-center gap-2">
                        <button
                          onClick={() => handleToggleLock(mb.id)}
                          className="px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 text-[10px]"
                        >
                          {mb.locked ? "Unlock" : "Lock"}
                        </button>
                        <button
                          onClick={() => handleDeleteMailbox(mb.id)}
                          className="px-2 py-1 rounded bg-red-600/20 hover:bg-red-600/40 text-red-300 text-[10px]"
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* TAB 2: ALIASES */}
      {activeTab === "aliases" && (
        <div className="space-y-6">
          <div className="glass-panel p-6 rounded-2xl border border-slate-800">
            <h2 className="text-sm font-bold text-white mb-4">Provision Email Alias</h2>
            <form onSubmit={handleCreateAlias} className="flex gap-3 text-xs">
              <input
                type="email"
                value={newAliasAddr}
                onChange={(e) => setNewAliasAddr(e.target.value)}
                placeholder="alias@domain.com"
                required
                className="flex-1 px-4 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <input
                type="email"
                value={newTargetAddr}
                onChange={(e) => setNewTargetAddr(e.target.value)}
                placeholder="target@domain.com"
                required
                className="flex-1 px-4 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <button
                type="submit"
                className="py-2 px-5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium transition-colors"
              >
                + Add Alias
              </button>
            </form>
          </div>

          <div className="glass-panel rounded-2xl border border-slate-800 overflow-hidden">
            <div className="p-4 border-b border-slate-800 bg-slate-900/60 font-bold text-xs text-slate-300">
              Active Routing Aliases ({aliases.length})
            </div>

            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                    <th className="p-3">Alias Address</th>
                    <th className="p-3">Target Mailbox</th>
                    <th className="p-3">Domain</th>
                    <th className="p-3">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60 font-mono">
                  {aliases.map((al) => (
                    <tr key={al.id} className="hover:bg-slate-900/30">
                      <td className="p-3 font-bold text-emerald-400">{al.aliasAddress}</td>
                      <td className="p-3 text-slate-200">&rarr; {al.targetAddress}</td>
                      <td className="p-3 text-slate-400">{al.domain}</td>
                      <td className="p-3">
                        <button
                          onClick={() => handleDeleteAlias(al.id)}
                          className="px-2 py-1 rounded bg-red-600/20 hover:bg-red-600/40 text-red-300 text-[10px]"
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* TAB 3: CSV BULK IMPORT */}
      {activeTab === "csv" && (
        <div className="glass-panel p-6 rounded-2xl border border-slate-800 space-y-4 text-xs">
          <div>
            <h2 className="text-sm font-bold text-white mb-1">CSV Bulk Provisioning</h2>
            <p className="text-slate-400">
              Import multiple mailboxes by pasting CSV content. Format: <code>email, role, quota_mb</code>
            </p>
          </div>

          {csvSuccessMessage && (
            <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-300">
              {csvSuccessMessage}
            </div>
          )}

          <textarea
            rows={6}
            value={csvText}
            onChange={(e) => setCsvText(e.target.value)}
            placeholder={`email,role,quota_mb\nalice@example.com,USER,5000\nbob@example.com,DOMAIN_ADMIN,2000`}
            className="w-full px-4 py-3 rounded-lg bg-slate-900 border border-slate-800 font-mono text-slate-200 placeholder-slate-600 focus:outline-none focus:border-emerald-500"
          />

          <div className="flex gap-3">
            <button
              onClick={handleParseCsv}
              className="py-2 px-4 rounded-lg bg-slate-800 hover:bg-slate-700 text-white font-medium transition-colors"
            >
              Preview CSV
            </button>

            {csvPreview.length > 0 && (
              <button
                onClick={handleExecuteCsvImport}
                className="py-2 px-5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium transition-colors"
              >
                Execute Bulk Import ({csvPreview.length} Accounts)
              </button>
            )}
          </div>

          {csvPreview.length > 0 && (
            <div className="mt-4 border border-slate-800 rounded-xl overflow-hidden bg-slate-950 p-4 space-y-2">
              <h3 className="font-bold text-white">Import Preview ({csvPreview.length} rows)</h3>
              <div className="divide-y divide-slate-800 font-mono text-[11px]">
                {csvPreview.map((row, idx) => (
                  <div key={idx} className="py-2 flex justify-between">
                    <span className="text-emerald-400">{row.email}</span>
                    <span className="text-slate-300">Role: {row.role}</span>
                    <span className="text-slate-400">Quota: {row.quotaMb} MB</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

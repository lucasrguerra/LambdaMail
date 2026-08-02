"use client";

import React, { useCallback, useEffect, useState } from "react";
import {
  Users,
  Mail,
  FileSpreadsheet,
  Plus,
  Lock,
  Unlock,
  Trash2,
  ShieldCheck,
  ArrowRight,
  Upload,
  AlertCircle,
  CheckCircle2,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card, CardHeader, CardTitle } from "../../../../components/ui/Card";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";

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

interface ApiMailbox {
  id: string;
  email_address: string;
  role: string;
  used_bytes: number;
  quota_bytes: number;
  mfa_enrolled: boolean;
  is_active: boolean;
}

interface ApiAlias {
  id: string;
  source_address: string;
  destination_addresses: string[];
  domain_name: string;
}

export default function AdminMailboxesPage() {
  const t = useTranslations();

  const [activeTab, setActiveTab] = useState<"mailboxes" | "aliases" | "csv">("mailboxes");
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [aliases, setAliases] = useState<Alias[]>([]);
  const [mfaPolicy, setMfaPolicy] = useState<"optional" | "required_admins" | "required_all">("required_admins");

  // Create mailbox state
  const [newEmail, setNewEmail] = useState("");
  const [newRole, setNewRole] = useState("USER");
  const [newQuota, setNewQuota] = useState(2000);
  const [newPassword, setNewPassword] = useState("");

  // Create alias state
  const [newAliasAddr, setNewAliasAddr] = useState("");
  const [newTargetAddr, setNewTargetAddr] = useState("");

  // CSV Import state
  const [csvText, setCsvText] = useState("");
  const [csvPreview, setCsvPreview] = useState<{ email: string; role: string; quotaMb: number }[]>([]);
  const [csvSuccessMessage, setCsvSuccessMessage] = useState<string | null>(null);

  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [mb, al] = await Promise.all([
        fetch("/api/v1/admin/mailboxes").then((r) => (r.ok ? r.json() : [])),
        fetch("/api/v1/admin/aliases").then((r) => (r.ok ? r.json() : [])),
      ]);
      setMailboxes(
        (mb as ApiMailbox[]).map((m) => ({
          id: m.id,
          email: m.email_address,
          role: m.role,
          storageUsedMb: Math.round(Number(m.used_bytes ?? 0) / 1048576),
          quotaMb: Math.round(Number(m.quota_bytes ?? 0) / 1048576),
          mfaEnabled: Boolean(m.mfa_enrolled),
          locked: !m.is_active,
        }))
      );
      setAliases(
        (al as ApiAlias[]).map((a) => ({
          id: a.id,
          aliasAddress: a.source_address,
          targetAddress: (a.destination_addresses ?? []).join(", "),
          domain: a.domain_name,
        }))
      );
    } catch {
      setError(t("errors.loadFailed"));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const handleCreateMailbox = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const localPart = newEmail.split("@")[0];
    const res = await fetch("/api/v1/admin/mailboxes", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        local_part: localPart,
        password: newPassword,
        role: newRole,
        quota_bytes: newQuota * 1048576,
      }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setError(data.message ?? "Could not create the mailbox.");
      return;
    }
    setNewEmail("");
    setNewPassword("");
    await load();
  };

  const handleToggleLock = async (id: string, locked: boolean) => {
    await fetch(`/api/v1/admin/mailboxes/${id}/active`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ is_active: locked }),
    });
    await load();
  };

  const handleDeleteMailbox = async (id: string) => {
    const res = await fetch(`/api/v1/admin/mailboxes/${id}`, { method: "DELETE" });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      setError(data.message ?? "Could not delete the mailbox.");
    }
    await load();
  };

  const handleCreateAlias = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const res = await fetch("/api/v1/admin/aliases", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source: newAliasAddr, destinations: [newTargetAddr] }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setError(data.message ?? "Could not create the alias.");
      return;
    }
    setNewAliasAddr("");
    setNewTargetAddr("");
    await load();
  };

  const handleDeleteAlias = async (id: string) => {
    const res = await fetch(`/api/v1/admin/aliases/${id}`, { method: "DELETE" });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      setError(data.message ?? "Could not delete the alias.");
    }
    await load();
  };

  const handleParseCsv = () => {
    const lines = csvText.split("\n").map((l) => l.trim()).filter(Boolean);
    const parsed: { email: string; role: string; quotaMb: number }[] = [];
    for (const line of lines) {
      if (line.startsWith("email") || line.startsWith("#")) continue;
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

  const handleExecuteCsvImport = async () => {
    try {
      const rows = csvPreview.map((item) => ({
        email: item.email,
        role: item.role as "USER" | "DOMAIN_ADMIN" | "SUPER_ADMIN",
        quota_mb: item.quotaMb,
      }));
      const res = await fetch("/api/v1/admin/mailboxes/bulk-import", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ rows }),
      });
      const data = await res.json().catch(() => ({}));
      if (res.ok) {
        setCsvSuccessMessage(`Imported ${data.imported ?? csvPreview.length} mailboxes.`);
        setCsvText("");
        setCsvPreview([]);
        await load();
      } else {
        setError(data.message ?? "Falha ao importar CSV");
      }
    } catch {
      setError(t("errors.serverError"));
    }
    setTimeout(() => setCsvSuccessMessage(null), 4000);
  };

  return (
    <div className="p-6 md:p-8 space-y-8 max-w-7xl mx-auto">
      {/* Header & Tabs */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl md:text-3xl font-extrabold text-white tracking-tight">
            {t("admin.mailboxesTitle")}
          </h1>
          <p className="text-sm text-slate-400 mt-1">
            {t("admin.mailboxesSubtitle")}
          </p>
        </div>

        {/* Tab Selection Navigation */}
        <div className="flex bg-slate-900/90 p-1.5 rounded-xl border border-slate-800 text-xs font-medium self-start md:self-auto">
          <button
            onClick={() => setActiveTab("mailboxes")}
            className={`flex items-center gap-2 px-3.5 py-2 rounded-lg transition-all ${
              activeTab === "mailboxes"
                ? "bg-emerald-600 text-white shadow-sm"
                : "text-slate-400 hover:text-slate-200"
            }`}
          >
            <Users className="w-3.5 h-3.5" />
            <span>Contas ({mailboxes.length})</span>
          </button>

          <button
            onClick={() => setActiveTab("aliases")}
            className={`flex items-center gap-2 px-3.5 py-2 rounded-lg transition-all ${
              activeTab === "aliases"
                ? "bg-emerald-600 text-white shadow-sm"
                : "text-slate-400 hover:text-slate-200"
            }`}
          >
            <Mail className="w-3.5 h-3.5" />
            <span>Aliases ({aliases.length})</span>
          </button>

          <button
            onClick={() => setActiveTab("csv")}
            className={`flex items-center gap-2 px-3.5 py-2 rounded-lg transition-all ${
              activeTab === "csv"
                ? "bg-emerald-600 text-white shadow-sm"
                : "text-slate-400 hover:text-slate-200"
            }`}
          >
            <FileSpreadsheet className="w-3.5 h-3.5" />
            <span>CSV</span>
          </button>
        </div>
      </div>

      {error && (
        <div className="p-4 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-300 text-sm flex items-center gap-3">
          <AlertCircle className="w-5 h-5 text-rose-400 flex-shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {/* Domain MFA Policy Panel */}
      <Card>
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
          <div>
            <h2 className="text-sm font-bold text-slate-100 flex items-center gap-2">
              <ShieldCheck className="w-4 h-4 text-emerald-400" />
              {t("admin.mfaPolicyTitle")}
            </h2>
            <p className="text-xs text-slate-400 mt-0.5">
              {t("admin.mfaPolicyIntro")}
            </p>
          </div>

          <div className="flex gap-2 text-xs">
            <button
              onClick={() => setMfaPolicy("optional")}
              className={`px-3 py-1.5 rounded-lg border font-medium transition-all ${
                mfaPolicy === "optional"
                  ? "bg-slate-800 border-slate-600 text-white"
                  : "border-slate-800 text-slate-400 hover:text-slate-200"
              }`}
            >
              {t("admin.mfaOptional")}
            </button>
            <button
              onClick={() => setMfaPolicy("required_admins")}
              className={`px-3 py-1.5 rounded-lg border font-medium transition-all ${
                mfaPolicy === "required_admins"
                  ? "bg-emerald-600/20 border-emerald-500 text-emerald-300"
                  : "border-slate-800 text-slate-400 hover:text-slate-200"
              }`}
            >
              {t("admin.mfaAdmins")}
            </button>
            <button
              onClick={() => setMfaPolicy("required_all")}
              className={`px-3 py-1.5 rounded-lg border font-medium transition-all ${
                mfaPolicy === "required_all"
                  ? "bg-indigo-600/20 border-indigo-500 text-indigo-300"
                  : "border-slate-800 text-slate-400 hover:text-slate-200"
              }`}
            >
              {t("admin.mfaEveryone")}
            </button>
          </div>
        </div>
      </Card>

      {/* TAB 1: MAILBOXES */}
      {activeTab === "mailboxes" && (
        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <Plus className="w-4 h-4 text-emerald-400" />
                {t("ui.provisionMailbox")}
              </CardTitle>
            </CardHeader>
            <form onSubmit={handleCreateMailbox} className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3 text-xs">
              <input
                type="email"
                value={newEmail}
                onChange={(e) => setNewEmail(e.target.value)}
                placeholder="nova.conta@domain.com"
                required
                className="px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <input
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                placeholder={t("ui.newPassword")}
                minLength={12}
                required
                className="px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <select
                value={newRole}
                onChange={(e) => setNewRole(e.target.value)}
                className="px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-emerald-500"
              >
                <option value="USER">USER</option>
                <option value="DOMAIN_ADMIN">DOMAIN_ADMIN</option>
                <option value="SUPER_ADMIN">SUPER_ADMIN</option>
              </select>
              <input
                type="number"
                value={newQuota}
                onChange={(e) => setNewQuota(parseInt(e.target.value, 10))}
                placeholder={t("ui.storageQuota")}
                className="px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-emerald-500"
              />
              <Button type="submit" variant="primary" size="md">
                {t("admin.createAccount")}
              </Button>
            </form>
          </Card>

          <Card className="p-0 overflow-hidden">
            <div className="p-4 border-b border-slate-800 bg-slate-900/80 font-bold text-xs text-slate-300 flex items-center justify-between">
              <span>Contas Cadastradas ({mailboxes.length})</span>
            </div>

            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                    <th className="p-3.5">{t("ui.emailAddress")}</th>
                    <th className="p-3.5">{t("ui.accountStatus")}</th>
                    <th className="p-3.5">{t("ui.storageQuota")}</th>
                    <th className="p-3.5">{t("ui.twoFactorSection")}</th>
                    <th className="p-3.5">{t("ui.accountStatus")}</th>
                    <th className="p-3.5">{t("ui.actions")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60 font-mono">
                  {mailboxes.map((mb) => (
                    <tr key={mb.id} className="hover:bg-slate-900/40 transition-colors">
                      <td className="p-3.5 font-bold text-slate-100">{mb.email}</td>
                      <td className="p-3.5 text-slate-300">
                        <Badge variant="neutral">{mb.role}</Badge>
                      </td>
                      <td className="p-3.5">
                        <div className="flex items-center gap-2">
                          <div className="w-24 h-2 bg-slate-800 rounded-full overflow-hidden">
                            <div
                              className="h-full bg-emerald-500 rounded-full"
                              style={{ width: `${Math.min((mb.storageUsedMb / (mb.quotaMb || 1)) * 100, 100)}%` }}
                            />
                          </div>
                          <span className="text-[10px] text-slate-400">
                            {mb.storageUsedMb}MB / {mb.quotaMb}MB
                          </span>
                        </div>
                      </td>
                      <td className="p-3.5">
                        <Badge variant={mb.mfaEnabled ? "success" : "warning"}>
                          {mb.mfaEnabled ? "TOTP ATIVO" : "SEM 2FA"}
                        </Badge>
                      </td>
                      <td className="p-3.5">
                        <Badge variant={mb.locked ? "danger" : "success"}>
                          {mb.locked ? "BLOQUEADA" : t("ui.active")}
                        </Badge>
                      </td>
                      <td className="p-3.5 flex items-center gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => void handleToggleLock(mb.id, mb.locked)}
                        >
                          {mb.locked ? <Unlock className="w-3 h-3" /> : <Lock className="w-3 h-3" />}
                          <span>{mb.locked ? "Desbloquear" : "Bloquear"}</span>
                        </Button>
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => void handleDeleteMailbox(mb.id)}
                        >
                          <Trash2 className="w-3 h-3" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        </div>
      )}

      {/* TAB 2: ALIASES */}
      {activeTab === "aliases" && (
        <div className="space-y-6">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <Plus className="w-4 h-4 text-emerald-400" />
                {t("admin.createAlias")}
              </CardTitle>
            </CardHeader>
            <form onSubmit={handleCreateAlias} className="flex flex-col sm:flex-row gap-3 text-xs">
              <input
                type="email"
                value={newAliasAddr}
                onChange={(e) => setNewAliasAddr(e.target.value)}
                placeholder="alias@domain.com"
                required
                className="flex-1 px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <input
                type="email"
                value={newTargetAddr}
                onChange={(e) => setNewTargetAddr(e.target.value)}
                placeholder="destino@domain.com"
                required
                className="flex-1 px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
              <Button type="submit" variant="primary" size="md">
                {t("admin.addAlias")}
              </Button>
            </form>
          </Card>

          <Card className="p-0 overflow-hidden">
            <div className="p-4 border-b border-slate-800 bg-slate-900/80 font-bold text-xs text-slate-300">
              Aliases de Roteamento ({aliases.length})
            </div>

            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse text-xs">
                <thead>
                  <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400">
                    <th className="p-3.5">{t("ui.emailAddress")}</th>
                    <th className="p-3.5">{t("ui.destination")}</th>
                    <th className="p-3.5">{t("admin.domainsTitle")}</th>
                    <th className="p-3.5">{t("ui.actions")}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60 font-mono">
                  {aliases.map((al) => (
                    <tr key={al.id} className="hover:bg-slate-900/40 transition-colors">
                      <td className="p-3.5 font-bold text-emerald-400">{al.aliasAddress}</td>
                      <td className="p-3.5 text-slate-200 flex items-center gap-2">
                        <ArrowRight className="w-3.5 h-3.5 text-slate-500" />
                        <span>{al.targetAddress}</span>
                      </td>
                      <td className="p-3.5 text-slate-400">{al.domain}</td>
                      <td className="p-3.5">
                        <Button
                          variant="danger"
                          size="sm"
                          onClick={() => void handleDeleteAlias(al.id)}
                        >
                          <Trash2 className="w-3 h-3" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        </div>
      )}

      {/* TAB 3: CSV BULK IMPORT */}
      {activeTab === "csv" && (
        <Card className="space-y-4">
          <div>
            <h2 className="text-base font-bold text-white flex items-center gap-2 mb-1">
              <Upload className="w-5 h-5 text-indigo-400" />
              Bulk mailbox import (CSV)
            </h2>
            <p className="text-xs text-slate-400">
              Paste CSV to provision several accounts with their RFC 6154 folders. Format: <code>email, role, quota_mb</code>
            </p>
          </div>

          {csvSuccessMessage && (
            <div className="p-3.5 rounded-xl bg-emerald-500/15 border border-emerald-500/30 text-emerald-300 text-xs flex items-center gap-2">
              <CheckCircle2 className="w-4 h-4 text-emerald-400" />
              <span>{csvSuccessMessage}</span>
            </div>
          )}

          <textarea
            rows={6}
            value={csvText}
            onChange={(e) => setCsvText(e.target.value)}
            placeholder={`email,role,quota_mb\nalice@example.com,USER,5000\nbob@example.com,DOMAIN_ADMIN,2000`}
            className="w-full px-4 py-3 rounded-xl bg-slate-900/90 border border-slate-800 font-mono text-xs text-slate-200 placeholder-slate-600 focus:outline-none focus:border-emerald-500"
          />

          <div className="flex gap-3">
            <Button variant="secondary" size="md" onClick={handleParseCsv}>
              {t("admin.csvPreview")}
            </Button>

            {csvPreview.length > 0 && (
              <Button variant="primary" size="md" onClick={handleExecuteCsvImport}>
                Run import ({csvPreview.length} Contas)
              </Button>
            )}
          </div>

          {csvPreview.length > 0 && (
            <div className="mt-4 border border-slate-800 rounded-xl overflow-hidden bg-slate-950 p-4 space-y-2">
              <h3 className="font-bold text-xs text-white">Preview ({csvPreview.length} linhas)</h3>
              <div className="divide-y divide-slate-800/80 font-mono text-[11px]">
                {csvPreview.map((row, idx) => (
                  <div key={idx} className="py-2 flex justify-between items-center">
                    <span className="text-emerald-400">{row.email}</span>
                    <Badge variant="neutral">{row.role}</Badge>
                    <span className="text-slate-400">Cota: {row.quotaMb} MB</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}

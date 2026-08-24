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
  Pencil,
  Search,
  X,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Pagination } from "../../../../components/ui/Pagination";
import { useDebounced } from "../../../../lib/useDebounced";
import { initialsFor } from "../../../../lib/initials";
import { Button } from "../../../../components/ui/Button";

interface Mailbox {
  id: string;
  email: string;
  role: string;
  storageUsedMb: number;
  quotaMb: number;
  mfaEnabled: boolean;
  locked: boolean;
  locale: string;
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
  locale: string | null;
}

/** The envelope every server-paged list answers with. */
interface PagedUsers {
  items: ApiMailbox[];
  total: number;
  page: number;
  page_size: number;
  total_pages: number;
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

  // Paging and filtering are the server's job; nothing here slices a list that
  // was never fully fetched in the first place.
  const [page, setPage] = useState(1);
  const [pageInfo, setPageInfo] = useState({ total: 0, page: 1, pageSize: 25, totalPages: 1 });
  const [search, setSearch] = useState("");
  const [roleFilter, setRoleFilter] = useState("");
  const [activeFilter, setActiveFilter] = useState("");
  const debouncedSearch = useDebounced(search, 300);

  // The user being edited, and the aliases that deliver to them.
  const [editing, setEditing] = useState<Mailbox | null>(null);
  const [editRole, setEditRole] = useState("USER");
  const [editQuotaMb, setEditQuotaMb] = useState(0);
  const [editLocale, setEditLocale] = useState("");
  const [editAliases, setEditAliases] = useState<Alias[] | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      const params = new URLSearchParams({ page: String(page), page_size: "25" });
      if (debouncedSearch) params.set("search", debouncedSearch);
      if (roleFilter) params.set("role", roleFilter);
      if (activeFilter) params.set("active", activeFilter);

      const [mb, al] = await Promise.all([
        fetch(`/api/v1/admin/mailboxes?${params.toString()}`).then((r) =>
          r.ok ? r.json() : { items: [], total: 0, page: 1, page_size: 25, total_pages: 1 },
        ),
        fetch("/api/v1/admin/aliases").then((r) => (r.ok ? r.json() : [])),
      ]);
      const users = mb as PagedUsers;
      setPageInfo({
        total: users.total ?? 0,
        page: users.page ?? 1,
        pageSize: users.page_size ?? 25,
        totalPages: users.total_pages ?? 1,
      });
      setMailboxes(
        (users.items ?? []).map((m) => ({
          id: m.id,
          email: m.email_address,
          role: m.role,
          storageUsedMb: Math.round(Number(m.used_bytes ?? 0) / 1048576),
          quotaMb: Math.round(Number(m.quota_bytes ?? 0) / 1048576),
          mfaEnabled: Boolean(m.mfa_enrolled),
          locked: !m.is_active,
          locale: m.locale ?? "",
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
  }, [t, page, debouncedSearch, roleFilter, activeFilter]);

  // A changed filter belongs on page one; keeping the page number lands the
  // reader on an empty page of a smaller result set.
  useEffect(() => {
    setPage(1);
  }, [debouncedSearch, roleFilter, activeFilter]);

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

  /**
   * Opens one user for editing, with the aliases that deliver to them.
   *
   * An alias belongs to a domain rather than to a person - it carries a list
   * of destinations, which may point at other servers - so what is shown here
   * is the set that lands in this mailbox, not a collection the user owns.
   */
  const openUser = async (mb: Mailbox) => {
    setEditing(mb);
    setEditRole(mb.role);
    setEditQuotaMb(mb.quotaMb);
    setEditLocale(mb.locale);
    setEditAliases(null);
    try {
      const res = await fetch(`/api/v1/admin/mailboxes/${encodeURIComponent(mb.id)}/aliases`);
      const data = res.ok ? await res.json() : [];
      setEditAliases(
        (data as ApiAlias[]).map((a) => ({
          id: a.id,
          aliasAddress: a.source_address,
          targetAddress: (a.destination_addresses ?? []).join(", "),
          domain: a.domain_name,
        })),
      );
    } catch {
      setEditAliases([]);
    }
  };

  const saveUser = async () => {
    if (!editing) return;
    setSaving(true);
    setError(null);
    try {
      const res = await fetch(`/api/v1/admin/mailboxes/${encodeURIComponent(editing.id)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          role: editRole,
          // Stored in bytes; the field is in megabytes because that is what an
          // operator thinks in.
          quota_bytes: Math.max(0, Math.round(editQuotaMb * 1048576)),
          locale: editLocale || undefined,
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setSaving(false);
    }
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
        setCsvSuccessMessage(t("admin.csvImported", { count: data.imported ?? csvPreview.length }));
        setCsvText("");
        setCsvPreview([]);
        await load();
      } else {
        setError(data.message ?? t("admin.csvImportFailed"));
      }
    } catch {
      setError(t("errors.serverError"));
    }
    setTimeout(() => setCsvSuccessMessage(null), 4000);
  };

  /* The tab labels, the lock buttons and the two-factor and lock states used to
     be literal strings - some Portuguese, some English - so half this screen
     stayed in one language whatever the interface was set to. They all come
     from the bundle now. */
  const tabs = [
    { id: "mailboxes" as const, label: `${t("admin.tabAccounts")} (${mailboxes.length})`, icon: Users },
    { id: "aliases" as const, label: `${t("admin.tabAliases")} (${aliases.length})`, icon: Mail },
    { id: "csv" as const, label: t("admin.tabCsv"), icon: FileSpreadsheet },
  ];

  const mfaPolicies = [
    { id: "optional" as const, label: t("admin.mfaOptional") },
    { id: "required_admins" as const, label: t("admin.mfaAdmins") },
    { id: "required_all" as const, label: t("admin.mfaEveryone") },
  ];

  const panel = "flex flex-col gap-4 rounded-2xl bg-dark-panel p-[18px] shadow-edge";
  const input =
    "w-full min-h-[36px] rounded-[10px] bg-dark-card px-3 py-2 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";
  const fieldLabel = "mb-1.5 block text-xs text-slate-400";
  const iconButton =
    "flex h-[30px] w-[30px] flex-none items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/[0.07] hover:text-slate-100";

  return (
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-[18px] px-5 pb-11 pt-7 sm:px-8">
      {/* Header & Tabs */}
      <div>
        <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("admin.mailboxesTitle")}</h1>
        <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.mailboxesSubtitle")}</p>
      </div>

      <div className="lm-tabstrip self-start">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              data-active={activeTab === tab.id}
              aria-pressed={activeTab === tab.id}
              className="lm-tab"
            >
              <Icon className="h-[15px] w-[15px] flex-none" />
              <span>{tab.label}</span>
            </button>
          );
        })}
      </div>

      {error && (
        <div className="flex items-center gap-2.5 rounded-xl bg-rose-900/60 px-4 py-3 text-[12.5px] leading-relaxed text-rose-200 shadow-edge">
          <AlertCircle className="h-4 w-4 flex-none" />
          <span>{error}</span>
        </div>
      )}

      {/* Domain MFA Policy Panel */}
      <div className="flex flex-wrap items-center gap-3.5 rounded-2xl bg-dark-panel px-[18px] py-4 shadow-edge">
        <div className="min-w-[240px] flex-1">
          <div className="flex items-center gap-2 text-[15px] font-medium leading-snug text-slate-100">
            <ShieldCheck className="h-4 w-4 flex-none text-indigo-500" />
            {t("admin.mfaPolicyTitle")}
          </div>
          <p className="mt-1 text-[12.5px] leading-relaxed text-slate-400">{t("admin.mfaPolicyIntro")}</p>
        </div>

        {/* One segmented control instead of three buttons in three different
            colours: these are three values of one setting, not three actions. */}
        <div className="flex flex-none flex-wrap gap-[3px] rounded-[10px] bg-dark-card p-[3px] shadow-edge">
          {mfaPolicies.map((policy) => (
            <button
              key={policy.id}
              type="button"
              onClick={() => setMfaPolicy(policy.id)}
              aria-pressed={mfaPolicy === policy.id}
              className={`rounded-lg px-3 py-1.5 text-[12.5px] leading-snug transition-colors ${
                mfaPolicy === policy.id
                  ? "text-indigo-500 shadow-edge-accent"
                  : "text-slate-400 hover:text-slate-100"
              }`}
            >
              {policy.label}
            </button>
          ))}
        </div>
      </div>

      {/* TAB 1: MAILBOXES */}
      {activeTab === "mailboxes" && (
        <div className="flex flex-col gap-3.5">
          <section className={panel}>
            <h2 className="text-[17px] font-medium leading-tight text-slate-100">
              {t("ui.provisionMailbox")}
            </h2>
            <form onSubmit={handleCreateMailbox} className="flex flex-wrap items-end gap-2.5">
              <div className="min-w-[220px] flex-[2]">
                <label htmlFor="new-email" className={fieldLabel}>
                  {t("ui.emailAddress")}
                </label>
                <input
                  id="new-email"
                  type="email"
                  value={newEmail}
                  onChange={(e) => setNewEmail(e.target.value)}
                  placeholder="nova.conta@domain.com"
                  required
                  className={input}
                />
              </div>
              <div className="min-w-[180px] flex-1">
                <label htmlFor="new-mailbox-password" className={fieldLabel}>
                  {t("ui.newPassword")}
                </label>
                <input
                  id="new-mailbox-password"
                  type="password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  minLength={12}
                  required
                  className={input}
                />
              </div>
              <div className="min-w-[160px] flex-1">
                <label htmlFor="new-role" className={fieldLabel}>
                  {t("ui.role")}
                </label>
                <select
                  id="new-role"
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value)}
                  className={input}
                >
                  <option value="USER">USER</option>
                  <option value="DOMAIN_ADMIN">DOMAIN_ADMIN</option>
                  <option value="SUPER_ADMIN">SUPER_ADMIN</option>
                </select>
              </div>
              <div className="min-w-[130px] flex-1">
                <label htmlFor="new-quota" className={fieldLabel}>
                  {t("admin.quotaMb")}
                </label>
                <input
                  id="new-quota"
                  type="number"
                  value={newQuota}
                  onChange={(e) => setNewQuota(parseInt(e.target.value, 10))}
                  className={input}
                />
              </div>
              <Button type="submit" variant="primary" size="md" className="flex-none">
                <Plus className="h-4 w-4" />
                <span>{t("admin.createAccount")}</span>
              </Button>
            </form>
          </section>

          <div className="rounded-2xl bg-dark-panel px-[18px] pb-3 pt-1.5 shadow-edge">
          {/* Filtering and paging are done by the server. */}
          <div className="flex flex-wrap items-center gap-2.5">
            <div className="relative min-w-[220px] flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
              <input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t("ui.searchPlaceholder")}
                aria-label={t("ui.searchPlaceholder")}
                className="w-full rounded-[10px] border border-white/[0.14] bg-transparent py-2 pl-9 pr-3 text-[13px] text-slate-100 placeholder:text-slate-500"
              />
            </div>
            <select
              value={roleFilter}
              onChange={(e) => setRoleFilter(e.target.value)}
              aria-label={t("ui.role")}
              className="rounded-[10px] border border-white/[0.14] bg-dark-panel px-3 py-2 text-[13px] text-slate-100"
            >
              <option value="">{t("ui.all")}</option>
              <option value="USER">USER</option>
              <option value="DOMAIN_ADMIN">DOMAIN_ADMIN</option>
              <option value="SUPER_ADMIN">SUPER_ADMIN</option>
            </select>
            <select
              value={activeFilter}
              onChange={(e) => setActiveFilter(e.target.value)}
              aria-label={t("ui.accountStatus")}
              className="rounded-[10px] border border-white/[0.14] bg-dark-panel px-3 py-2 text-[13px] text-slate-100"
            >
              <option value="">{t("ui.all")}</option>
              <option value="true">{t("ui.active")}</option>
              <option value="false">{t("ui.accountLocked")}</option>
            </select>
          </div>

            <div className="overflow-x-auto">
              <table className="lm-table">
                <thead>
                  <tr>
                    <th className="pl-0">{t("ui.emailAddress")}</th>
                    <th>{t("ui.role")}</th>
                    <th>{t("ui.storageQuota")}</th>
                    <th>{t("ui.twoFactorSection")}</th>
                    <th>{t("ui.accountStatus")}</th>
                    <th className="pr-0 text-right">{t("ui.actions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {mailboxes.map((mb) => (
                    <tr key={mb.id}>
                      <td className="pl-0">
                        <div className="flex items-center gap-2.5">
                          <span className="flex h-7 w-7 flex-none items-center justify-center rounded-full bg-indigo-900 text-[11px] uppercase text-indigo-300">
                            {initialsFor(mb.email)}
                          </span>
                          <span className="break-words text-[13.5px] text-slate-100">{mb.email}</span>
                        </div>
                      </td>
                      <td>
                        <Badge variant="neutral" className="whitespace-nowrap">
                          {mb.role}
                        </Badge>
                      </td>
                      <td>
                        <div className="flex min-w-[150px] items-center gap-2.5">
                          <span className="block h-1.5 flex-1 overflow-hidden rounded-full bg-dark-card">
                            <span
                              className="block h-full rounded-full bg-indigo-500"
                              style={{
                                width: `${Math.min((mb.storageUsedMb / (mb.quotaMb || 1)) * 100, 100)}%`,
                              }}
                            />
                          </span>
                          <span className="whitespace-nowrap text-[11.5px] tabular-nums text-slate-400">
                            {mb.storageUsedMb} / {mb.quotaMb} MB
                          </span>
                        </div>
                      </td>
                      <td>
                        <Badge variant={mb.mfaEnabled ? "success" : "warning"} className="whitespace-nowrap">
                          {mb.mfaEnabled ? t("ui.mfaActive") : t("ui.mfaNone")}
                        </Badge>
                      </td>
                      <td>
                        <Badge variant={mb.locked ? "danger" : "info"} className="whitespace-nowrap">
                          {mb.locked ? t("ui.accountLocked") : t("ui.active")}
                        </Badge>
                      </td>
                      <td className="whitespace-nowrap pr-0 text-right">
                        <button
                          type="button"
                          onClick={() => void openUser(mb)}
                          title={t("common.edit")}
                          aria-label={t("common.edit")}
                          className={`${iconButton} inline-flex`}
                        >
                          <Pencil className="h-[15px] w-[15px]" />
                        </button>
                        <button
                          type="button"
                          onClick={() => void handleToggleLock(mb.id, mb.locked)}
                          title={mb.locked ? t("admin.unlockAccount") : t("admin.lockAccount")}
                          aria-label={mb.locked ? t("admin.unlockAccount") : t("admin.lockAccount")}
                          className={`${iconButton} inline-flex`}
                        >
                          {mb.locked ? <Unlock className="h-[15px] w-[15px]" /> : <Lock className="h-[15px] w-[15px]" />}
                        </button>
                        <button
                          type="button"
                          onClick={() => void handleDeleteMailbox(mb.id)}
                          title={t("common.delete")}
                          aria-label={t("common.delete")}
                          className={`${iconButton} inline-flex hover:text-rose-400`}
                        >
                          <Trash2 className="h-[15px] w-[15px]" />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <Pagination
              page={pageInfo.page}
              pageSize={pageInfo.pageSize}
              total={pageInfo.total}
              totalPages={pageInfo.totalPages}
              onPage={setPage}
            />
          </div>
        </div>
      )}

      {/* The user being edited, with the aliases that land in their mailbox. */}
      {editing && (
        <div className="fixed inset-0 z-40 flex items-start justify-center overflow-y-auto bg-black/60 p-4 pt-16">
          <div className="w-full max-w-[560px] rounded-2xl bg-dark-panel p-5 shadow-edge">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <h2 className="break-words text-[17px] font-medium leading-tight text-slate-100">
                  {editing.email}
                </h2>
                <p className="mt-1 text-[12px] text-slate-400">{t("ui.editUser")}</p>
              </div>
              <button
                type="button"
                onClick={() => setEditing(null)}
                aria-label={t("common.cancel")}
                className={`${iconButton} inline-flex`}
              >
                <X className="h-[15px] w-[15px]" />
              </button>
            </div>

            <div className="mt-4 flex flex-col gap-3">
              <div>
                <label htmlFor="edit-role" className={fieldLabel}>{t("ui.role")}</label>
                <select
                  id="edit-role"
                  value={editRole}
                  onChange={(e) => setEditRole(e.target.value)}
                  className={input}
                >
                  <option value="USER">USER</option>
                  <option value="DOMAIN_ADMIN">DOMAIN_ADMIN</option>
                  <option value="SUPER_ADMIN">SUPER_ADMIN</option>
                </select>
              </div>
              <div>
                <label htmlFor="edit-quota" className={fieldLabel}>{t("ui.quotaMb")}</label>
                <input
                  id="edit-quota"
                  type="number"
                  min={0}
                  value={editQuotaMb}
                  onChange={(e) => setEditQuotaMb(Number(e.target.value))}
                  className={input}
                />
              </div>
              <div>
                <label htmlFor="edit-locale" className={fieldLabel}>{t("settings.language")}</label>
                <select
                  id="edit-locale"
                  value={editLocale}
                  onChange={(e) => setEditLocale(e.target.value)}
                  className={input}
                >
                  <option value="">{t("common.none")}</option>
                  <option value="pt-BR">Portugues (BR)</option>
                  <option value="en">English</option>
                  <option value="es">Espanol</option>
                </select>
              </div>

              <div className="rounded-xl bg-dark-card p-3.5 shadow-edge">
                <div className="text-xs text-slate-400">{t("admin.aliasesForUser")}</div>
                {editAliases === null ? (
                  <div className="mt-2 text-[12.5px] text-slate-500">{t("common.loading")}</div>
                ) : editAliases.length === 0 ? (
                  <div className="mt-2 text-[12.5px] text-slate-500">{t("admin.noAliasesForUser")}</div>
                ) : (
                  <ul className="mt-2 flex flex-col gap-1.5">
                    {editAliases.map((a) => (
                      <li key={a.id} className="break-all font-mono text-[12px] text-slate-200">
                        {a.aliasAddress}
                      </li>
                    ))}
                  </ul>
                )}
              </div>

              <div className="flex justify-end gap-2 pt-1">
                <Button variant="secondary" size="sm" onClick={() => setEditing(null)}>
                  {t("common.cancel")}
                </Button>
                <Button variant="primary" size="sm" onClick={() => void saveUser()} disabled={saving}>
                  {saving ? t("common.loading") : t("common.save")}
                </Button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* TAB 2: ALIASES */}
      {activeTab === "aliases" && (
        <div className="flex flex-col gap-3.5">
          <section className={panel}>
            <h2 className="text-[17px] font-medium leading-tight text-slate-100">{t("admin.createAlias")}</h2>
            <form onSubmit={handleCreateAlias} className="flex flex-wrap items-end gap-3">
              <div className="min-w-[220px] flex-1">
                <label htmlFor="new-alias" className={fieldLabel}>
                  {t("admin.aliasesTitle")}
                </label>
                <input
                  id="new-alias"
                  type="email"
                  value={newAliasAddr}
                  onChange={(e) => setNewAliasAddr(e.target.value)}
                  placeholder="alias@domain.com"
                  required
                  className={input}
                />
              </div>
              <ArrowRight className="mb-2.5 h-[18px] w-[18px] flex-none text-slate-500" />
              <div className="min-w-[220px] flex-1">
                <label htmlFor="new-alias-target" className={fieldLabel}>
                  {t("admin.aliasTarget")}
                </label>
                <input
                  id="new-alias-target"
                  type="email"
                  value={newTargetAddr}
                  onChange={(e) => setNewTargetAddr(e.target.value)}
                  placeholder="destino@domain.com"
                  required
                  className={input}
                />
              </div>
              <Button type="submit" variant="primary" size="md" className="flex-none">
                {t("admin.addAlias")}
              </Button>
            </form>
          </section>

          <div className="rounded-2xl bg-dark-panel px-[18px] pb-3 pt-1.5 shadow-edge">
            <div className="overflow-x-auto">
              <table className="lm-table">
                <thead>
                  <tr>
                    <th className="pl-0">{t("admin.aliasesTitle")}</th>
                    <th>{t("ui.destination")}</th>
                    <th>{t("admin.domainsTitle")}</th>
                    <th className="pr-0 text-right">{t("ui.actions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {aliases.map((al) => (
                    <tr key={al.id}>
                      <td className="break-words pl-0 text-[13.5px] text-slate-100">{al.aliasAddress}</td>
                      <td className="break-words text-[13px] text-slate-300">{al.targetAddress}</td>
                      <td className="text-[13px] text-slate-400">{al.domain}</td>
                      <td className="pr-0 text-right">
                        <button
                          type="button"
                          onClick={() => void handleDeleteAlias(al.id)}
                          title={t("common.delete")}
                          aria-label={t("common.delete")}
                          className={`${iconButton} inline-flex hover:text-rose-400`}
                        >
                          <Trash2 className="h-[15px] w-[15px]" />
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
        <div className="flex flex-wrap gap-3.5">
          <section className={`${panel} min-w-[320px] flex-1`}>
            <div>
              <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
                <Upload className="h-[17px] w-[17px] flex-none text-indigo-500" />
                {t("admin.csvTitle")}
              </h2>
              <p className="mt-1 text-[13px] leading-relaxed text-slate-400">{t("admin.csvIntro")}</p>
            </div>

            {csvSuccessMessage && (
              <div className="flex items-center gap-2 rounded-xl bg-dark-card px-3.5 py-3 text-[12.5px] text-slate-200 shadow-edge">
                <CheckCircle2 className="h-4 w-4 flex-none text-indigo-500" />
                <span>{csvSuccessMessage}</span>
              </div>
            )}

            <textarea
              rows={8}
              value={csvText}
              onChange={(e) => setCsvText(e.target.value)}
              aria-label={t("admin.csvTitle")}
              placeholder={`email,role,quota_mb\nalice@example.com,USER,5000\nbob@example.com,DOMAIN_ADMIN,2000`}
              className="lm-code w-full px-4 py-3 text-slate-200 placeholder-slate-600 focus:outline-none focus-visible:shadow-edge-accent"
            />

            <div className="flex flex-wrap gap-2">
              <Button variant="secondary" size="md" onClick={handleParseCsv}>
                {t("admin.csvPreview")}
              </Button>
              {csvPreview.length > 0 && (
                <Button variant="primary" size="md" onClick={handleExecuteCsvImport}>
                  {t("admin.csvRun")} ({csvPreview.length})
                </Button>
              )}
            </div>
          </section>

          {csvPreview.length > 0 && (
            <section className={`${panel} min-w-[300px] flex-1`}>
              <div className="flex items-center justify-between gap-2.5">
                <h3 className="text-[17px] font-medium leading-tight text-slate-100">
                  {t("admin.csvPreview")}
                </h3>
                <Badge variant="neutral">{csvPreview.length}</Badge>
              </div>
              <div className="flex flex-col gap-2">
                {csvPreview.map((row, idx) => (
                  <div
                    key={idx}
                    className="flex flex-wrap items-center gap-2.5 rounded-xl bg-dark-card px-3 py-2.5 shadow-edge"
                  >
                    <span className="min-w-0 flex-1 break-words text-[13px] text-slate-100">{row.email}</span>
                    <Badge variant="neutral" className="flex-none whitespace-nowrap">
                      {row.role}
                    </Badge>
                    <span className="flex-none whitespace-nowrap text-[11.5px] tabular-nums text-slate-400">
                      {row.quotaMb} MB
                    </span>
                  </div>
                ))}
              </div>
            </section>
          )}
        </div>
      )}
    </div>
  );
}

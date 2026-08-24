"use client";

import React, { useCallback, useEffect, useState } from "react";
import { PlayCircle, Server, User as UserIcon, Search } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";
import { useDebounced } from "../../../../lib/useDebounced";

/**
 * Checks an operator runs by hand, against the server or one user.
 *
 * Everything here reads state; nothing sends mail or changes a setting. A
 * diagnostic screen that alters what it is diagnosing is worse than no screen.
 */

interface Check {
  key: string;
  status: "PASS" | "WARN" | "FAIL" | "INFO";
  subject?: string;
  detail?: string;
}

interface Report {
  scope: string;
  subject?: string;
  status: Check["status"];
  checks: Check[];
}

interface UserRow {
  id: string;
  email_address: string;
}

function variantFor(status: Check["status"]): "success" | "warning" | "danger" | "neutral" {
  if (status === "PASS") return "success";
  if (status === "WARN") return "warning";
  if (status === "FAIL") return "danger";
  return "neutral";
}

export default function AdminTestsPage() {
  const t = useTranslations();

  const [serverReport, setServerReport] = useState<Report | null>(null);
  const [userReport, setUserReport] = useState<Report | null>(null);
  const [runningServer, setRunningServer] = useState(false);
  const [runningUser, setRunningUser] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [search, setSearch] = useState("");
  const [users, setUsers] = useState<UserRow[]>([]);
  const [selected, setSelected] = useState("");
  const debouncedSearch = useDebounced(search, 300);

  // The picker asks the server for a short page rather than pulling every
  // user into the browser to filter them here.
  const loadUsers = useCallback(async () => {
    try {
      const params = new URLSearchParams({ page: "1", page_size: "20" });
      if (debouncedSearch) params.set("search", debouncedSearch);
      const res = await fetch(`/api/v1/admin/mailboxes?${params.toString()}`);
      if (!res.ok) throw new Error();
      const data = await res.json();
      setUsers((data.items ?? []) as UserRow[]);
    } catch {
      setUsers([]);
    }
  }, [debouncedSearch]);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const runServer = async () => {
    setRunningServer(true);
    setError(null);
    try {
      const res = await fetch("/api/v1/admin/diagnostics/server");
      if (!res.ok) throw new Error();
      setServerReport(await res.json());
    } catch {
      setServerReport(null);
      setError(t("errors.loadFailed"));
    } finally {
      setRunningServer(false);
    }
  };

  const runUser = async () => {
    if (!selected) return;
    setRunningUser(true);
    setError(null);
    try {
      const res = await fetch(`/api/v1/admin/diagnostics/user/${encodeURIComponent(selected)}`);
      if (!res.ok) throw new Error();
      setUserReport(await res.json());
    } catch {
      setUserReport(null);
      setError(t("errors.loadFailed"));
    } finally {
      setRunningUser(false);
    }
  };

  const panel = "flex flex-col gap-3.5 rounded-2xl bg-dark-panel p-[18px] shadow-edge";

  const renderReport = (report: Report | null) =>
    report === null ? null : (
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-2.5">
          <Badge variant={variantFor(report.status)}>{report.status}</Badge>
          <span className="text-[12px] text-slate-400">
            {t("admin.checksRun", { count: report.checks.length })}
          </span>
        </div>

        <ul className="flex flex-col gap-1.5">
          {report.checks.map((c, i) => (
            <li
              key={`${c.key}-${c.subject ?? i}`}
              className="flex flex-wrap items-center gap-2.5 rounded-xl bg-dark-card px-3.5 py-2.5 shadow-edge"
            >
              <Badge variant={variantFor(c.status)} className="whitespace-nowrap">
                {c.status}
              </Badge>
              {/* The key is translated here rather than sent as a finished
                  sentence, so the page reads in the interface's language. */}
              <span className="min-w-0 flex-1 break-words text-[13px] text-slate-200">
                {t(`checks.${c.key}`)}
                {c.subject ? <span className="text-slate-400"> — {c.subject}</span> : null}
              </span>
              {c.detail ? (
                <span className="whitespace-nowrap font-mono text-[11.5px] text-slate-400">{c.detail}</span>
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    );

  return (
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-[18px] px-5 pb-11 pt-7 sm:px-8">
      <div className="min-w-0">
        <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("admin.testsTitle")}</h1>
        <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.testsSubtitle")}</p>
      </div>

      {error && (
        <div className="rounded-xl bg-rose-900/60 px-4 py-3 text-[12.5px] text-rose-200 shadow-edge">{error}</div>
      )}

      <section className={panel}>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
            <Server className="h-[17px] w-[17px] flex-none text-indigo-500" />
            {t("admin.serverChecks")}
          </h2>
          <Button variant="primary" size="sm" onClick={() => void runServer()} disabled={runningServer}>
            <PlayCircle className="h-3.5 w-3.5" />
            <span>{runningServer ? t("common.loading") : t("admin.runChecks")}</span>
          </Button>
        </div>
        {renderReport(serverReport)}
      </section>

      <section className={panel}>
        <h2 className="flex items-center gap-2 text-[17px] font-medium leading-tight text-slate-100">
          <UserIcon className="h-[17px] w-[17px] flex-none text-indigo-500" />
          {t("admin.userChecks")}
        </h2>

        <div className="flex flex-wrap items-center gap-2.5">
          <div className="relative min-w-[200px] flex-1">
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
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
            aria-label={t("admin.userChecks")}
            className="min-w-[220px] rounded-[10px] border border-white/[0.14] bg-dark-panel px-3 py-2 text-[13px] text-slate-100"
          >
            <option value="">{t("ui.selectUser")}</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.email_address}
              </option>
            ))}
          </select>
          <Button
            variant="primary"
            size="sm"
            onClick={() => void runUser()}
            disabled={runningUser || !selected}
          >
            <PlayCircle className="h-3.5 w-3.5" />
            <span>{runningUser ? t("common.loading") : t("admin.runChecks")}</span>
          </Button>
        </div>

        {userReport?.subject ? (
          <div className="break-all text-[12.5px] text-slate-400">{userReport.subject}</div>
        ) : null}
        {renderReport(userReport)}
      </section>
    </div>
  );
}

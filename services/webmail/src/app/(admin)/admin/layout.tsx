"use client";

import React from "react";
import Link from "next/link";
import { useTranslations } from "../../../i18n/provider";
import { LanguageSwitcher } from "../../../i18n/LanguageSwitcher";

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const t = useTranslations();
  return (
    <div className="flex h-screen overflow-hidden bg-slate-950 text-slate-100">
      {/* Executive Sidebar */}
      <aside className="w-64 border-r border-emerald-950/60 bg-slate-900/80 flex flex-col justify-between p-4">
        <div>
          {/* Admin Surface Branding */}
          <div className="flex items-center gap-3 px-2 mb-6">
            <div className="w-8 h-8 rounded-lg bg-emerald-600/30 border border-emerald-500/40 flex items-center justify-center text-emerald-400 font-bold">
              &#9881;
            </div>
            <div>
              <div className="font-bold text-sm text-white">LambdaMail Admin</div>
              <div className="text-[10px] text-emerald-400 uppercase tracking-wider font-mono">{t("common.adminPortal")}</div>
            </div>
          </div>

          <div className="mb-6 p-2 rounded-lg bg-emerald-500/10 border border-emerald-500/20 text-[11px] text-emerald-300">
            <strong>Isolated Surface</strong>
            <div className="text-[10px] text-emerald-400/80 font-mono">aud: lambdamail:admin</div>
          </div>

          {/* Admin Navigation */}
          <nav className="space-y-1 text-sm font-medium">
            <Link
              href="/admin/dashboard"
              className="flex items-center gap-2.5 px-3 py-2 rounded-lg bg-emerald-600/10 text-emerald-300 border border-emerald-500/20"
            >
              <span>&#128202;</span> {t("admin.dashboardTitle")}
            </Link>

            <Link
              href="/admin/domains"
              className="flex items-center gap-2.5 px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <span>&#127760;</span> {t("admin.domainsTitle")}
            </Link>

            <Link
              href="/admin/mailboxes"
              className="flex items-center gap-2.5 px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <span>&#128100;</span> {t("admin.mailboxesTitle")}
            </Link>

            <Link
              href="/admin/queue"
              className="flex items-center gap-2.5 px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <span>&#128238;</span> {t("admin.queueTitle")}
            </Link>

            <Link
              href="/admin/dmarc"
              className="flex items-center gap-2.5 px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <span>&#128200;</span> {t("admin.dmarcTitle")}
            </Link>

            <Link
              href="/admin/security"
              className="flex items-center gap-2.5 px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <span>&#128737;</span> {t("admin.securityTitle")}
            </Link>
          </nav>
        </div>

        {/* Language and session controls */}
        <div className="mb-3 flex items-center justify-between gap-2">
          <LanguageSwitcher />
          <button
            type="button"
            onClick={() => {
              void fetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
                window.location.href = "/admin/login";
              });
            }}
            className="rounded-md border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:text-white"
          >
            {t("settings.signOut")}
          </button>
        </div>
        {/* Operator Profile Footer */}
        <div className="pt-4 border-t border-slate-800">
          <div className="flex items-center justify-between p-2 rounded-xl bg-slate-900 border border-slate-800">
            <div className="flex items-center gap-2.5 min-w-0">
              <div className="w-7 h-7 rounded-full bg-emerald-500/20 text-emerald-400 flex items-center justify-center font-bold text-xs flex-shrink-0">
                A
              </div>
              <div className="truncate">
                <div className="text-xs font-medium text-slate-200 truncate">admin@lambdamail.local</div>
                <div className="text-[10px] text-emerald-400">SUPER_ADMIN (2FA)</div>
              </div>
            </div>
          </div>
        </div>
      </aside>

      {/* Main Content Area */}
      <main className="flex-1 flex flex-col overflow-y-auto bg-slate-950">
        {children}
      </main>
    </div>
  );
}

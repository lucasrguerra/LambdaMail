"use client";

import React from "react";
import Link from "next/link";
import { useTranslations } from "../../../i18n/provider";
import { LanguageSwitcher } from "../../../i18n/LanguageSwitcher";

export default function UserWebmailLayout({ children }: { children: React.ReactNode }) {
  const t = useTranslations();
  return (
    <div className="flex h-screen overflow-hidden bg-slate-950 text-slate-100">
      {/* Sidebar Navigation */}
      <aside className="w-64 border-r border-slate-800 bg-slate-900/50 flex flex-col justify-between p-4">
        <div>
          {/* Webmail Surface Branding */}
          <div className="flex items-center gap-3 px-2 mb-6">
            <div className="w-8 h-8 rounded-lg bg-indigo-600/30 border border-indigo-500/40 flex items-center justify-center text-indigo-400 font-bold">
              @
            </div>
            <div>
              <div className="font-bold text-sm text-white">LambdaMail</div>
              <div className="text-[10px] text-slate-400 uppercase tracking-wider font-mono">{t("common.userPortal")}</div>
            </div>
          </div>

          <Link
            href="/user/compose"
            className="flex items-center justify-center gap-2 w-full py-2.5 px-4 mb-6 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-sm transition-colors shadow-lg shadow-indigo-600/20"
          >
            <span>+</span> {t("mail.compose")}
          </Link>

          {/* Folder Navigation List */}
          <nav className="space-y-1 text-sm font-medium">
            <Link
              href="/user/mail/inbox"
              className="flex items-center justify-between px-3 py-2 rounded-lg bg-indigo-600/10 text-indigo-300 border border-indigo-500/20"
            >
              <div className="flex items-center gap-2.5">
                <span>&#128236;</span> {t("mail.inbox")}
              </div>
              <span className="text-xs bg-indigo-500/30 px-2 py-0.5 rounded-full font-bold">3</span>
            </Link>

            <Link
              href="/user/mail/sent"
              className="flex items-center justify-between px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-2.5">
                <span>&#128234;</span> {t("mail.sent")}
              </div>
            </Link>

            <Link
              href="/user/mail/drafts"
              className="flex items-center justify-between px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-2.5">
                <span>&#128221;</span> {t("mail.drafts")}
              </div>
              <span className="text-xs bg-slate-800 px-2 py-0.5 rounded-full">1</span>
            </Link>

            <Link
              href="/user/mail/archive"
              className="flex items-center justify-between px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-2.5">
                <span>&#128451;</span> Archive
              </div>
            </Link>

            <Link
              href="/user/mail/junk"
              className="flex items-center justify-between px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-2.5">
                <span>&#9888;</span> Spam
              </div>
            </Link>

            <Link
              href="/user/mail/trash"
              className="flex items-center justify-between px-3 py-2 rounded-lg text-slate-400 hover:text-slate-200 hover:bg-slate-800/50 transition-colors"
            >
              <div className="flex items-center gap-2.5">
                <span>&#128465;</span> Trash
              </div>
            </Link>
          </nav>
        </div>

        {/* User Settings & Account Footer */}
        {/* Language and session controls */}
        <div className="mb-3 flex items-center justify-between gap-2">
          <LanguageSwitcher />
          <button
            type="button"
            onClick={() => {
              void fetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
                window.location.href = "/user/login";
              });
            }}
            className="rounded-md border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:text-white"
          >
            {t("settings.signOut")}
          </button>
        </div>
        <div className="pt-4 border-t border-slate-800">
          <Link
            href="/user/settings"
            className="flex items-center justify-between p-2 rounded-xl bg-slate-900 border border-slate-800 hover:border-slate-700 transition-colors"
          >
            <div className="flex items-center gap-2.5 min-w-0">
              <div className="w-7 h-7 rounded-full bg-indigo-500/20 text-indigo-400 flex items-center justify-center font-bold text-xs flex-shrink-0">
                U
              </div>
              <div className="truncate">
                <div className="text-xs font-medium text-slate-200 truncate">user@lambdamail.local</div>
                <div className="text-[10px] text-slate-400">Settings & 2FA</div>
              </div>
            </div>
            <span className="text-slate-400 text-xs">&#9881;</span>
          </Link>
        </div>
      </aside>

      {/* Main Content Area */}
      <main className="flex-1 flex flex-col overflow-hidden bg-slate-950">
        {children}
      </main>
    </div>
  );
}

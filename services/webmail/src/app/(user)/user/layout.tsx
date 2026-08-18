"use client";

import React, { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { motion, AnimatePresence } from "framer-motion";
import {
  Inbox,
  Send,
  FileText,
  Archive,
  AlertTriangle,
  Trash2,
  Settings,
  PenSquare,
  LogOut,
  Mail,
  ShieldCheck,
  ShieldAlert,
  Sliders,
  Menu,
  X,
} from "lucide-react";
import { useTranslations } from "../../../i18n/provider";
import { LanguageSwitcher } from "../../../i18n/LanguageSwitcher";
import { useAccount, isAdminRole } from "../../../lib/useAccount";
import { useFolders } from "../../../lib/useFolders";
import { badgeCount, folderMetrics } from "../../../lib/mailCounts";

export default function UserWebmailLayout({ children }: { children: React.ReactNode }) {
  const t = useTranslations();
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  const account = useAccount("user");
  // Live, not fetched once: these badges used to freeze at the value they had
  // when the tab was opened.
  const { folders } = useFolders();

  const navItems = [
    { href: "/user/mail/inbox", label: t("mail.inbox"), icon: Inbox, role: "inbox" },
    { href: "/user/mail/sent", label: t("mail.sent"), icon: Send, role: "sent" },
    { href: "/user/mail/drafts", label: t("mail.drafts"), icon: FileText, role: "drafts" },
    { href: "/user/mail/archive", label: t("mail.archive"), icon: Archive, role: "archive" },
    { href: "/user/mail/junk", label: t("mail.junk"), icon: AlertTriangle, role: "junk" },
    { href: "/user/mail/trash", label: t("mail.trash"), icon: Trash2, role: "trash" },
  ];

  const handleLogout = () => {
    void fetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
      window.location.href = "/user/login";
    });
  };

  const SidebarContent = (
    <div className="flex flex-col justify-between h-full p-4">
      <div>
        {/* Webmail Surface Branding */}
        <div className="flex items-center gap-3 px-2 mb-6">
          <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-indigo-600 to-cyan-500 border border-indigo-400/30 flex items-center justify-center text-white font-bold shadow-lg shadow-indigo-500/20">
            <Mail className="w-5 h-5 text-white" />
          </div>
          <div>
            <div className="font-bold text-base text-white tracking-tight">LambdaMail</div>
            <div className="text-[10px] text-indigo-400 uppercase tracking-widest font-mono font-semibold">
              {t("common.userPortal")}
            </div>
          </div>
        </div>

        {/* Compose Floating Button */}
        <Link
          href="/user/compose"
          className="group relative flex items-center justify-center gap-2 w-full py-3 px-4 mb-6 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-semibold text-sm transition-all duration-200 shadow-lg shadow-indigo-600/30 active:scale-[0.98]"
        >
          <PenSquare className="w-4 h-4 transition-transform group-hover:rotate-12" />
          <span>{t("mail.compose")}</span>
        </Link>

        {/* Folder Navigation List */}
        <nav className="space-y-1 text-sm font-medium">
          {navItems.map((item) => {
            const Icon = item.icon;
            const isActive = pathname === item.href || (item.role === "inbox" && pathname === "/user/mail");
            // Drafts counts what is waiting rather than what is unread; see
            // badgeCount, which is where that rule is tested.
            const badge = badgeCount(folders, item.role);
            const { total } = folderMetrics(folders, item.role);

            return (
              <Link
                key={item.href}
                href={item.href}
                title={total > 0 ? t("mail.messagesInFolder", { count: total }) : item.label}
                className={`flex items-center justify-between px-3.5 py-2.5 rounded-xl transition-all duration-150 ${
                  isActive
                    ? "bg-indigo-600/20 text-indigo-300 border border-indigo-500/30 shadow-sm"
                    : "text-slate-400 hover:text-slate-200 hover:bg-slate-800/40 border border-transparent"
                }`}
              >
                <div className="flex items-center gap-3 min-w-0">
                  <Icon className={`w-4 h-4 flex-shrink-0 ${isActive ? "text-indigo-400" : "text-slate-400"}`} />
                  <span className="truncate">{item.label}</span>
                </div>
                <div className="flex items-center gap-1.5 flex-shrink-0">
                  {/* The folder's size, shown quietly beside the badge: the
                      sidebar reported unread only, so there was nowhere in the
                      interface that said how much mail a folder holds. */}
                  {total > 0 && (
                    <span className="text-[10px] font-mono text-slate-500 tabular-nums">{total}</span>
                  )}
                  {badge > 0 && (
                    <span
                      className={`text-xs px-2 py-0.5 rounded-full font-semibold tabular-nums ${
                        isActive ? "bg-indigo-500/40 text-indigo-200" : "bg-indigo-500/20 text-indigo-300"
                      }`}
                    >
                      {badge}
                    </span>
                  )}
                </div>
              </Link>
            );
          })}
        </nav>
      </div>

      {/* Footer Controls & User Settings */}
      <div className="space-y-3">
        <div className="flex items-center justify-between gap-2 p-2 rounded-xl bg-slate-900/60 border border-slate-800/80">
          <LanguageSwitcher />
          <button
            type="button"
            onClick={handleLogout}
            className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg border border-slate-700/80 text-xs font-medium text-slate-300 hover:text-rose-400 hover:border-rose-500/40 hover:bg-rose-500/10 transition-all"
          >
            <LogOut className="w-3.5 h-3.5" />
            <span>{t("settings.signOut")}</span>
          </button>
        </div>

        {/* The console is reachable from inside the app for the accounts that
            may open it. It goes to the step-up rather than to the admin sign-in:
            the admin audience is a separate token, so crossing over costs a
            second factor - but not the password, which the session in hand
            proved already. */}
        {isAdminRole(account?.role) && (
          <Link
            href="/admin/step-up"
            className="flex items-center gap-2.5 p-2.5 rounded-xl border border-emerald-500/25 bg-emerald-500/10 text-emerald-300 hover:border-emerald-500/50 hover:bg-emerald-500/15 transition-all"
          >
            <Sliders className="w-4 h-4 flex-shrink-0" />
            <span className="text-xs font-semibold truncate">{t("admin.openAdmin")}</span>
          </Link>
        )}

        <Link
          href="/user/settings"
          className={`flex items-center justify-between p-2.5 rounded-xl border transition-all ${
            pathname === "/user/settings"
              ? "bg-indigo-600/15 border-indigo-500/30 text-slate-100"
              : "bg-slate-900/80 border-slate-800 hover:border-slate-700 text-slate-300"
          }`}
        >
          <div className="flex items-center gap-2.5 min-w-0">
            <div className="w-8 h-8 rounded-full bg-gradient-to-tr from-indigo-500 to-cyan-500 text-white flex items-center justify-center font-bold text-xs flex-shrink-0 shadow-sm uppercase">
              {account?.email?.[0] ?? "?"}
            </div>
            <div className="truncate">
              <div className="text-xs font-semibold text-slate-200 truncate">
                {account?.email ?? t("common.loading")}
              </div>
              {/* Reports what is actually configured. The old fixed "2FA
                  active" reassured accounts that had no second factor at all. */}
              <div className="text-[10px] text-slate-400 flex items-center gap-1">
                {account?.mfa_enrolled ? (
                  <>
                    <ShieldCheck className="w-3 h-3 text-emerald-400 inline" />
                    {t("settings.mfaEnabled")}
                  </>
                ) : (
                  <>
                    <ShieldAlert className="w-3 h-3 text-amber-400 inline" />
                    {t("settings.mfaDisabled")}
                  </>
                )}
              </div>
            </div>
          </div>
          <Settings className="w-4 h-4 text-slate-400 hover:text-slate-200 transition-colors" />
        </Link>
      </div>
    </div>
  );

  return (
    <div className="flex h-screen overflow-hidden bg-dark-bg text-slate-100">
      {/* Mobile Top Header */}
      <div className="md:hidden flex items-center justify-between p-4 border-b border-slate-800 bg-slate-900/90 z-20 w-full fixed top-0 left-0">
        <div className="flex items-center gap-2">
          <div className="w-8 h-8 rounded-lg bg-indigo-600 flex items-center justify-center text-white font-bold">
            <Mail className="w-4 h-4" />
          </div>
          <span className="font-bold text-slate-100">LambdaMail</span>
        </div>
        <button
          onClick={() => setMobileOpen(!mobileOpen)}
          aria-label={t("ui.toggleMenu")}
          aria-expanded={mobileOpen}
          className="p-2 rounded-lg bg-slate-800 text-slate-300 hover:text-white"
        >
          {mobileOpen ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
        </button>
      </div>

      {/* Desktop Sidebar */}
      <aside className="hidden md:flex w-64 border-r border-slate-800/80 bg-slate-900/60 backdrop-blur-xl flex-col flex-shrink-0">
        {SidebarContent}
      </aside>

      {/* Mobile Overlay Menu */}
      <AnimatePresence>
        {mobileOpen && (
          <motion.aside
            initial={{ x: "-100%" }}
            animate={{ x: 0 }}
            exit={{ x: "-100%" }}
            transition={{ type: "spring", damping: 25, stiffness: 200 }}
            className="fixed inset-0 z-30 bg-slate-950 w-72 border-r border-slate-800 pt-16 md:hidden"
          >
            {SidebarContent}
          </motion.aside>
        )}
      </AnimatePresence>

      {/* Main Content Area */}
      <main className="flex-1 flex flex-col overflow-hidden pt-16 md:pt-0 bg-dark-bg relative">
        {children}
      </main>
    </div>
  );
}

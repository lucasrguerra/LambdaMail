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
  PenSquare,
  LogOut,
  Mail,
  ShieldCheck,
  ShieldAlert,
  SlidersHorizontal,
  Menu,
  X,
} from "lucide-react";
import { useTranslations } from "../../../i18n/provider";
import { LanguageSwitcher } from "../../../i18n/LanguageSwitcher";
import { useAccount, isAdminRole } from "../../../lib/useAccount";
import { useFolders } from "../../../lib/useFolders";
import { badgeCount, folderMetrics } from "../../../lib/mailCounts";
import { initialsFor } from "../../../lib/initials";

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

  const settingsActive = pathname === "/user/settings";

  const SidebarContent = (
    <div className="flex h-full flex-col gap-4 p-3">
      {/* The brand mark. One mark for both surfaces - the line underneath is
          the only thing that says which one you are in. */}
      <div className="flex items-center gap-2.5 px-2 py-1">
        <div className="flex h-8 w-8 flex-none items-center justify-center rounded-[10px] bg-dark-card text-[17px] leading-none text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
          λ
        </div>
        <div className="min-w-0">
          <div className="text-[15px] font-medium leading-tight">LambdaMail</div>
          <div className="text-[10.5px] uppercase tracking-[0.08em] text-indigo-500">
            {t("common.userPortal")}
          </div>
        </div>
      </div>

      <Link
        href="/user/compose"
        onClick={() => setMobileOpen(false)}
        className="flex w-full items-center justify-center gap-2 rounded-[10px] border border-indigo-500 px-3 py-2.5 text-sm font-medium text-indigo-500 transition-colors hover:bg-indigo-500/[0.12] active:bg-indigo-500/[0.22]"
      >
        <PenSquare className="h-4 w-4 flex-none" />
        <span className="min-w-0">{t("mail.compose")}</span>
      </Link>

      {/* Folder navigation. Every row is a mark, an icon, a label that may wrap,
          and its numbers - nothing is sized by the length of the label, which
          is what clipped the Portuguese and Spanish names before. */}
      <nav className="flex flex-col gap-0.5">
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
              onClick={() => setMobileOpen(false)}
              data-active={isActive}
              title={total > 0 ? t("mail.messagesInFolder", { count: total }) : item.label}
              className="lm-nav"
            >
              <span className="lm-nav-mark" />
              <Icon className={`h-[17px] w-[17px] flex-none ${isActive ? "text-indigo-500" : "text-slate-400"}`} />
              <span className="min-w-0 flex-1 text-[13.5px] leading-snug">{item.label}</span>
              <span className="flex flex-none items-center gap-1.5">
                {/* The folder's size, shown quietly beside the badge: the
                    sidebar reported unread only, so there was nowhere in the
                    interface that said how much mail a folder holds. */}
                {total > 0 && badge > 0 && (
                  <span className="text-[10.5px] tabular-nums text-slate-500">{total}</span>
                )}
                {badge > 0 ? (
                  <span
                    className={`text-[11.5px] tabular-nums ${isActive ? "text-indigo-300" : "text-slate-400"}`}
                  >
                    {badge}
                  </span>
                ) : (
                  total > 0 && (
                    <span className="text-[11.5px] tabular-nums text-slate-500">{total}</span>
                  )
                )}
              </span>
            </Link>
          );
        })}

        <div className="lm-rule my-2.5" />

        <Link
          href="/user/settings"
          onClick={() => setMobileOpen(false)}
          data-active={settingsActive}
          className="lm-nav"
        >
          <span className="lm-nav-mark" />
          <SlidersHorizontal
            className={`h-[17px] w-[17px] flex-none ${settingsActive ? "text-indigo-500" : "text-slate-400"}`}
          />
          <span className="min-w-0 flex-1 text-[13.5px] leading-snug">{t("settings.title")}</span>
        </Link>
      </nav>

      <div className="mt-auto flex flex-col gap-2.5">
        {/* The two surfaces as one control, so where you are and where else you
            could be are the same question. The console goes to the step-up
            rather than to the admin sign-in: the admin audience is a separate
            token, so crossing over costs a second factor - but not the
            password, which the session in hand proved already. */}
        {isAdminRole(account?.role) && (
          <div className="flex gap-[3px] rounded-[10px] bg-dark-panel p-[3px] shadow-edge">
            <span className="flex flex-1 items-center justify-center gap-1.5 rounded-[8px] bg-dark-card px-2 py-1.5 text-[12px] leading-snug text-indigo-500 shadow-edge-accent">
              <Mail className="h-3.5 w-3.5 flex-none" />
              <span className="min-w-0 truncate">{t("common.userPortal")}</span>
            </span>
            <Link
              href="/admin/step-up"
              className="flex flex-1 items-center justify-center gap-1.5 rounded-[8px] px-2 py-1.5 text-[12px] leading-snug text-slate-400 transition-colors hover:bg-white/[0.05] hover:text-slate-100"
            >
              <ShieldCheck className="h-3.5 w-3.5 flex-none" />
              <span className="min-w-0 truncate">{t("admin.openAdmin")}</span>
            </Link>
          </div>
        )}

        <Link
          href="/user/settings"
          onClick={() => setMobileOpen(false)}
          className="flex items-center gap-2.5 rounded-xl bg-dark-panel p-2.5 shadow-edge transition-colors hover:bg-dark-card"
        >
          <span className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-full bg-indigo-900 text-[12px] uppercase text-indigo-300 shadow-[inset_0_0_0_1px_#5d5294]">
            {initialsFor(account?.email)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block break-words text-[12.5px] leading-tight">
              {account?.email ?? t("common.loading")}
            </span>
            {/* Reports what is actually configured. The old fixed "2FA
                active" reassured accounts that had no second factor at all. */}
            <span className="mt-0.5 flex items-center gap-1 text-[10.5px] text-slate-400">
              {account?.mfa_enrolled ? (
                <>
                  <ShieldCheck className="h-3 w-3 flex-none text-indigo-500" />
                  {t("settings.mfaEnabled")}
                </>
              ) : (
                <>
                  <ShieldAlert className="h-3 w-3 flex-none text-amber-400" />
                  {t("settings.mfaDisabled")}
                </>
              )}
            </span>
          </span>
        </Link>

        <div className="flex items-center gap-1.5">
          <LanguageSwitcher />
          <button
            type="button"
            onClick={handleLogout}
            title={t("settings.signOut")}
            aria-label={t("settings.signOut")}
            className="flex h-8 w-8 flex-none items-center justify-center rounded-[9px] border border-white/[0.14] text-slate-300 transition-colors hover:bg-white/[0.07] hover:text-rose-400"
          >
            <LogOut className="h-[15px] w-[15px]" />
          </button>
        </div>
      </div>
    </div>
  );

  return (
    <div className="flex h-screen overflow-hidden bg-dark-bg text-slate-100">
      {/* Mobile Top Header */}
      <div className="fixed left-0 top-0 z-20 flex w-full items-center justify-between bg-dark-rail px-4 py-3 shadow-[inset_0_-1px_0_0_rgba(233,233,237,0.09)] md:hidden">
        <div className="flex items-center gap-2.5">
          <div className="flex h-8 w-8 items-center justify-center rounded-[10px] bg-dark-card text-[17px] leading-none text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
            λ
          </div>
          <span className="font-medium text-slate-100">LambdaMail</span>
        </div>
        <button
          onClick={() => setMobileOpen(!mobileOpen)}
          aria-label={t("ui.toggleMenu")}
          aria-expanded={mobileOpen}
          className="flex h-9 w-9 items-center justify-center rounded-[10px] text-slate-300 transition-colors hover:bg-white/[0.07] hover:text-slate-100"
        >
          {mobileOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
        </button>
      </div>

      {/* Desktop rail */}
      <aside className="hidden w-[244px] flex-none flex-col bg-dark-rail shadow-[inset_-1px_0_0_0_rgba(233,233,237,0.09)] md:flex">
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
            className="fixed inset-y-0 left-0 z-30 w-[280px] overflow-y-auto bg-dark-rail pt-14 shadow-[inset_-1px_0_0_0_rgba(233,233,237,0.09)] md:hidden"
          >
            {SidebarContent}
          </motion.aside>
        )}
      </AnimatePresence>

      {/* Main Content Area */}
      <main className="relative flex flex-1 flex-col overflow-hidden bg-dark-bg pt-14 md:pt-0">
        {children}
      </main>
    </div>
  );
}

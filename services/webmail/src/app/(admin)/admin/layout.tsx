"use client";

import React, { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { motion, AnimatePresence } from "framer-motion";
import {
  Gauge,
  Globe,
  Users,
  ListOrdered,
  Activity,
  ShieldCheck,
  LogOut,
  Menu,
  X,
  Lock,
  Mail,
} from "lucide-react";
import { useTranslations } from "../../../i18n/provider";
import { LanguageSwitcher } from "../../../i18n/LanguageSwitcher";
import { useAccount } from "../../../lib/useAccount";
import { initialsFor } from "../../../lib/initials";

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const t = useTranslations();
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  const account = useAccount("admin");

  const navItems = [
    { href: "/admin/dashboard", label: t("admin.dashboardTitle"), icon: Gauge },
    { href: "/admin/domains", label: t("admin.domainsTitle"), icon: Globe },
    { href: "/admin/mailboxes", label: t("admin.mailboxesTitle"), icon: Users },
    { href: "/admin/queue", label: t("admin.queueTitle"), icon: ListOrdered },
    { href: "/admin/dmarc", label: t("admin.dmarcTitle"), icon: Activity },
    { href: "/admin/security", label: t("admin.securityTitle"), icon: ShieldCheck },
  ];

  const handleLogout = () => {
    void fetch("/api/v1/auth/logout", { method: "POST" }).finally(() => {
      window.location.href = "/admin/login";
    });
  };

  /* The console's rail is the webmail's rail. It used to be green where the
     other was indigo, which made one product look like two; the only thing
     that now says which surface you are in is the line under the brand and
     which half of the switch is lit. */
  const SidebarContent = (
    <div className="flex h-full flex-col gap-4 p-3">
      <div className="flex items-center gap-2.5 px-2 py-1">
        <div className="flex h-8 w-8 flex-none items-center justify-center rounded-[10px] bg-dark-card text-[17px] leading-none text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
          λ
        </div>
        <div className="min-w-0">
          <div className="text-[15px] font-medium leading-tight">LambdaMail</div>
          <div className="text-[10.5px] uppercase tracking-[0.08em] text-indigo-500">
            {t("common.adminPortal")}
          </div>
        </div>
      </div>

      <nav className="flex flex-col gap-0.5">
        {navItems.map((item) => {
          const Icon = item.icon;
          const isActive = pathname === item.href;

          return (
            <Link
              key={item.href}
              href={item.href}
              onClick={() => setMobileOpen(false)}
              data-active={isActive}
              className="lm-nav"
            >
              <span className="lm-nav-mark" />
              <Icon className={`h-[17px] w-[17px] flex-none ${isActive ? "text-indigo-500" : "text-slate-400"}`} />
              <span className="min-w-0 flex-1 text-[13.5px] leading-snug">{item.label}</span>
            </Link>
          );
        })}
      </nav>

      <div className="mt-auto flex flex-col gap-2.5">
        {/* Leaving the console needs no ceremony - the webmail session was
            never given up to get here, so this is a plain link back. Coming the
            other way is what costs a second factor. */}
        <div className="flex gap-[3px] rounded-[10px] bg-dark-panel p-[3px] shadow-edge">
          <Link
            href="/user/mail/inbox"
            className="flex flex-1 items-center justify-center gap-1.5 rounded-[8px] px-2 py-1.5 text-[12px] leading-snug text-slate-400 transition-colors hover:bg-white/[0.05] hover:text-slate-100"
          >
            <Mail className="h-3.5 w-3.5 flex-none" />
            <span className="min-w-0 truncate">{t("admin.backToWebmail")}</span>
          </Link>
          <span className="flex flex-1 items-center justify-center gap-1.5 rounded-[8px] bg-dark-card px-2 py-1.5 text-[12px] leading-snug text-indigo-500 shadow-edge-accent">
            <ShieldCheck className="h-3.5 w-3.5 flex-none" />
            <span className="min-w-0 truncate">{t("common.adminPortal")}</span>
          </span>
        </div>

        <div className="flex items-center gap-2.5 rounded-xl bg-dark-panel p-2.5 shadow-edge">
          <span className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-full bg-indigo-900 text-[12px] uppercase text-indigo-300 shadow-[inset_0_0_0_1px_#5d5294]">
            {initialsFor(account?.email)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block break-words text-[12.5px] leading-tight">
              {account?.email ?? t("common.loading")}
            </span>
            <span className="mt-0.5 flex items-center gap-1 text-[10.5px] text-slate-400">
              <Lock className="h-3 w-3 flex-none text-indigo-500" />
              {account?.role ?? ""}
            </span>
          </span>
        </div>

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
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex h-8 w-8 flex-none items-center justify-center rounded-[10px] bg-dark-card text-[17px] leading-none text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
            λ
          </div>
          <span className="min-w-0 truncate font-medium text-slate-100">
            {t("common.appName")} {t("common.adminPortal")}
          </span>
        </div>
        <button
          onClick={() => setMobileOpen(!mobileOpen)}
          aria-label={t("ui.toggleMenu")}
          aria-expanded={mobileOpen}
          className="flex h-9 w-9 flex-none items-center justify-center rounded-[10px] text-slate-300 transition-colors hover:bg-white/[0.07] hover:text-slate-100"
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
      <main className="flex flex-1 flex-col overflow-y-auto bg-dark-bg pt-14 md:pt-0">
        {children}
      </main>
    </div>
  );
}

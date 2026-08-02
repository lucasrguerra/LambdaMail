"use client";

import React, { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { motion, AnimatePresence } from "framer-motion";
import {
  LayoutDashboard,
  Globe,
  Users,
  ListOrdered,
  Activity,
  ShieldCheck,
  LogOut,
  Sliders,
  Menu,
  X,
  Lock,
} from "lucide-react";
import { useTranslations } from "../../../i18n/provider";
import { LanguageSwitcher } from "../../../i18n/LanguageSwitcher";

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const t = useTranslations();
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);

  const navItems = [
    { href: "/admin/dashboard", label: t("admin.dashboardTitle"), icon: LayoutDashboard },
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

  const SidebarContent = (
    <div className="flex flex-col justify-between h-full p-4">
      <div>
        {/* Admin Surface Branding */}
        <div className="flex items-center gap-3 px-2 mb-6">
          <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-emerald-600 to-teal-500 border border-emerald-400/30 flex items-center justify-center text-white font-bold shadow-lg shadow-emerald-500/20">
            <Sliders className="w-5 h-5 text-white" />
          </div>
          <div>
            <div className="font-bold text-base text-white tracking-tight">LambdaMail</div>
            <div className="text-[10px] text-emerald-400 uppercase tracking-widest font-mono font-semibold">
              {t("common.adminPortal")}
            </div>
          </div>
        </div>

        {/* Security Badge Indicator */}
        <div className="mb-6 p-2.5 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-xs text-emerald-300 flex items-center gap-2">
          <Lock className="w-4 h-4 text-emerald-400 flex-shrink-0" />
          <div>
            <strong className="block text-[11px] font-semibold text-emerald-300">Isolated Surface</strong>
            <span className="text-[10px] text-emerald-400/80 font-mono">aud: lambdamail:admin</span>
          </div>
        </div>

        {/* Admin Navigation */}
        <nav className="space-y-1 text-sm font-medium">
          {navItems.map((item) => {
            const Icon = item.icon;
            const isActive = pathname === item.href;

            return (
              <Link
                key={item.href}
                href={item.href}
                className={`flex items-center gap-3 px-3.5 py-2.5 rounded-xl transition-all duration-150 ${
                  isActive
                    ? "bg-emerald-500/15 text-emerald-300 border border-emerald-500/30 shadow-sm"
                    : "text-slate-400 hover:text-slate-200 hover:bg-slate-800/40 border border-transparent"
                }`}
              >
                <Icon className={`w-4 h-4 ${isActive ? "text-emerald-400" : "text-slate-400"}`} />
                <span>{item.label}</span>
              </Link>
            );
          })}
        </nav>
      </div>

      {/* Language and session controls */}
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

        {/* Operator Profile Footer */}
        <div className="pt-3 border-t border-slate-800/80">
          <div className="flex items-center justify-between p-2.5 rounded-xl bg-slate-900 border border-slate-800">
            <div className="flex items-center gap-2.5 min-w-0">
              <div className="w-8 h-8 rounded-full bg-emerald-500/20 border border-emerald-500/30 text-emerald-400 flex items-center justify-center font-bold text-xs flex-shrink-0">
                A
              </div>
              <div className="truncate">
                <div className="text-xs font-semibold text-slate-200 truncate">admin@lambdamail.local</div>
                <div className="text-[10px] text-emerald-400 font-mono">SUPER_ADMIN (2FA)</div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );

  return (
    <div className="flex h-screen overflow-hidden bg-dark-bg text-slate-100">
      {/* Mobile Top Header */}
      <div className="md:hidden flex items-center justify-between p-4 border-b border-slate-800 bg-slate-900/90 z-20 w-full fixed top-0 left-0">
        <div className="flex items-center gap-2">
          <div className="w-8 h-8 rounded-lg bg-emerald-600 flex items-center justify-center text-white font-bold">
            <Sliders className="w-4 h-4" />
          </div>
          <span className="font-bold text-slate-100">LambdaMail Admin</span>
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

      {/* Desktop Executive Sidebar */}
      <aside className="hidden md:flex w-64 border-r border-emerald-950/60 bg-slate-900/70 backdrop-blur-xl flex-col flex-shrink-0">
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
      <main className="flex-1 flex flex-col overflow-y-auto pt-16 md:pt-0 bg-dark-bg">
        {children}
      </main>
    </div>
  );
}

"use client";

import React, { useState } from "react";
import { Mail, Lock, AlertCircle } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card } from "../../../../components/ui/Card";
import { Button } from "../../../../components/ui/Button";

export default function UserLoginPage() {
  const t = useTranslations();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  /**
   * One step, because the mailbox is one step.
   *
   * Reading your own mail is behind your own password; the second factor guards
   * the console, and is collected on the way in there. This screen used to have
   * a code step as well, so an account that had enrolled a factor could not open
   * its inbox without its phone.
   */
  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      const res = await fetch("/api/v1/auth/user/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.message || t("auth.invalidCredentials"));

      // The destination the middleware attached when it sent an unauthenticated
      // visitor here, so a deep link survives the sign-in.
      // A path on this origin only: "//evil.test" also begins with a slash, and
      // following it would turn the sign-in into an open redirect.
      const next = new URLSearchParams(window.location.search).get("next");
      const safe = next && next.startsWith("/") && !next.startsWith("//");
      window.location.href = safe ? next : "/user/mail/inbox";
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : t("auth.genericError"));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-dark-bg relative overflow-hidden">
      {/* Background Ambient Glow */}
      <div className="absolute -top-40 -left-40 w-96 h-96 bg-indigo-600/20 rounded-full blur-3xl pointer-events-none" />
      <div className="absolute -bottom-40 -right-40 w-96 h-96 bg-cyan-600/20 rounded-full blur-3xl pointer-events-none" />

      <Card className="max-w-md w-full border border-slate-800 shadow-2xl p-8 z-10">
        <div className="flex items-center gap-3.5 mb-6">
          <div className="w-11 h-11 rounded-2xl bg-gradient-to-tr from-indigo-600 to-cyan-500 flex items-center justify-center text-white font-bold shadow-lg shadow-indigo-500/20">
            <Mail className="w-6 h-6" />
          </div>
          <div>
            <h1 className="text-xl font-extrabold text-white tracking-tight">{t("auth.userLoginTitle")}</h1>
            <p className="text-xs text-slate-400">{t("common.appName")} &middot; {t("common.userPortal")}</p>
          </div>
        </div>

        {error && (
          <div className="mb-5 p-3.5 rounded-xl bg-rose-500/10 border border-rose-500/30 text-rose-300 text-xs flex items-center gap-2.5">
            <AlertCircle className="w-4 h-4 text-rose-400 flex-shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleLogin} className="space-y-4 text-xs">
          <div>
            <label htmlFor="login-email" className="block font-semibold text-slate-300 mb-1.5">
              {t("auth.emailLabel")}
            </label>
            <div className="relative">
              <Mail className="w-4 h-4 absolute left-3.5 top-3 text-slate-500" />
              <input
                id="login-email"
                type="email"
                autoComplete="username"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="user@domain.com"
                required
                className="w-full pl-10 pr-4 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
              />
            </div>
          </div>

          <div>
            <label htmlFor="login-password" className="block font-semibold text-slate-300 mb-1.5">
              {t("auth.passwordLabel")}
            </label>
            <div className="relative">
              <Lock className="w-4 h-4 absolute left-3.5 top-3 text-slate-500" />
              <input
                id="login-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="************"
                required
                className="w-full pl-10 pr-4 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
              />
            </div>
          </div>

          <Button type="submit" variant="primary" size="lg" className="w-full mt-2" disabled={loading}>
            {loading ? t("common.loading") : t("auth.signInButton")}
          </Button>
        </form>

      </Card>
    </div>
  );
}

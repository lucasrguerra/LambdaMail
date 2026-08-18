"use client";

import React, { useState } from "react";
import { Mail, Lock, AlertCircle } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
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

  const field =
    "w-full min-h-[38px] rounded-xl bg-dark-card py-2.5 pl-[38px] pr-3 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";

  return (
    /* No ambient glow behind the card any more: two blurred coloured circles
       under a sign-in form are decoration that costs a full-viewport paint and
       says nothing. Nocturne's depth is the surface and its edge. */
    <div className="flex min-h-screen items-center justify-center bg-dark-bg px-4">
      <div className="w-full max-w-[400px] rounded-2xl bg-dark-panel p-7 shadow-edge">
        <div className="mb-6 flex items-center gap-3">
          <div className="flex h-10 w-10 flex-none items-center justify-center rounded-xl bg-dark-card text-xl leading-none text-indigo-500 shadow-[inset_0_0_0_1px_#9184d9]">
            λ
          </div>
          <div className="min-w-0">
            <h1 className="text-lg font-medium leading-tight text-slate-100">{t("auth.userLoginTitle")}</h1>
            <p className="mt-0.5 text-[12.5px] text-slate-400">
              {t("common.appName")} &middot; {t("common.userPortal")}
            </p>
          </div>
        </div>

        {error && (
          <div className="mb-5 flex items-start gap-2.5 rounded-xl bg-rose-900/60 p-3.5 text-xs leading-relaxed text-rose-200 shadow-edge">
            <AlertCircle className="mt-px h-4 w-4 flex-none" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleLogin} className="flex flex-col gap-4">
          <div>
            <label htmlFor="login-email" className="mb-1.5 block text-xs text-slate-400">
              {t("auth.emailLabel")}
            </label>
            <div className="relative">
              <Mail className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
              <input
                id="login-email"
                type="email"
                autoComplete="username"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="user@domain.com"
                required
                className={field}
              />
            </div>
          </div>

          <div>
            <label htmlFor="login-password" className="mb-1.5 block text-xs text-slate-400">
              {t("auth.passwordLabel")}
            </label>
            <div className="relative">
              <Lock className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
              <input
                id="login-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="************"
                required
                className={field}
              />
            </div>
          </div>

          <Button type="submit" variant="primary" size="lg" className="mt-1 w-full" disabled={loading}>
            {loading ? t("common.loading") : t("auth.signInButton")}
          </Button>
        </form>
      </div>
    </div>
  );
}

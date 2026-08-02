"use client";

import React, { useState } from "react";
import Link from "next/link";
import { useTranslations } from "../../../../i18n/provider";

export default function UserLoginPage() {
  const t = useTranslations();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [challengeToken, setChallengeToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setLoading(true);

    try {
      if (challengeToken) {
        // Step 2: Verify TOTP code
        const res = await fetch("/api/v1/auth/mfa/verify", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ challenge_token: challengeToken, code: mfaCode }),
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || "Verification failed");
        window.location.href = "/user/mail/inbox";
        return;
      }

      // Step 1: Password authentication
      const res = await fetch("/api/v1/auth/user/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.message || "Authentication failed");

      if (data.mfa_required) {
        setChallengeToken(data.challenge_token);
      } else {
        window.location.href = "/user/mail/inbox";
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "An error occurred");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-slate-950">
      <div className="glass-panel p-8 rounded-2xl max-w-md w-full border border-slate-800 shadow-2xl">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-xl bg-indigo-600/20 border border-indigo-500/30 flex items-center justify-center text-indigo-400 font-bold text-xl">
            @
          </div>
          <div>
            <h1 className="text-xl font-bold text-white">{t("auth.userLoginTitle")}</h1>
            <p className="text-xs text-slate-400">{t("common.appName")} &middot; {t("common.userPortal")}</p>
          </div>
        </div>

        {error && (
          <div className="mb-4 p-3 rounded-lg bg-red-500/10 border border-red-500/30 text-red-400 text-sm">
            {error}
          </div>
        )}

        <form onSubmit={handleLogin} className="space-y-4">
          {!challengeToken ? (
            <>
              <div>
                <label className="block text-xs font-medium text-slate-300 mb-1">{t("auth.emailLabel")}</label>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="user@domain.com"
                  required
                  className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
                />
              </div>

              <div>
                <label className="block text-xs font-medium text-slate-300 mb-1">{t("auth.passwordLabel")}</label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="************"
                  required
                  className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
                />
              </div>
            </>
          ) : (
            <div>
              <div className="p-3 mb-4 rounded-lg bg-indigo-500/10 border border-indigo-500/30 text-indigo-300 text-xs">
                {t("auth.mfaRequired")}
              </div>
              <label className="block text-xs font-medium text-slate-300 mb-1">{t("auth.totpCodeLabel")}</label>
              <input
                type="text"
                value={mfaCode}
                onChange={(e) => setMfaCode(e.target.value)}
                placeholder="123456"
                maxLength={6}
                required
                className="w-full px-4 py-2.5 rounded-lg bg-slate-900 border border-slate-800 text-white text-center tracking-widest text-lg font-mono focus:outline-none focus:border-indigo-500 transition-colors"
              />
            </div>
          )}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-3 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium transition-colors shadow-lg shadow-indigo-600/20 disabled:opacity-50"
          >
            {loading ? t("common.loading") : challengeToken ? t("auth.verifyCodeButton") : t("auth.signInButton")}
          </button>
        </form>

        <div className="mt-6 pt-4 border-t border-slate-800 text-center">
          <Link href="/" className="text-xs text-slate-400 hover:text-slate-200 transition-colors">
            Back to Surface Selector
          </Link>
        </div>
      </div>
    </div>
  );
}

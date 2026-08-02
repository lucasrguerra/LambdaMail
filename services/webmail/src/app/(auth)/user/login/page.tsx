"use client";

import React, { useState } from "react";
import Link from "next/link";
import { Mail, Lock, ShieldCheck, ArrowLeft, KeyRound, AlertCircle } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card } from "../../../../components/ui/Card";
import { Button } from "../../../../components/ui/Button";

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
        if (!res.ok) throw new Error(data.message || "Two-factor verification failed");
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
      if (!res.ok) throw new Error(data.message || "Invalid credentials");

      if (data.mfa_required) {
        setChallengeToken(data.challenge_token);
      } else {
        window.location.href = "/user/mail/inbox";
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Ocorreu um erro ao autenticar");
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
          {!challengeToken ? (
            <>
              <div>
                <label className="block font-semibold text-slate-300 mb-1.5">{t("auth.emailLabel")}</label>
                <div className="relative">
                  <Mail className="w-4 h-4 absolute left-3.5 top-3 text-slate-500" />
                  <input
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="user@domain.com"
                    required
                    className="w-full pl-10 pr-4 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
                  />
                </div>
              </div>

              <div>
                <label className="block font-semibold text-slate-300 mb-1.5">{t("auth.passwordLabel")}</label>
                <div className="relative">
                  <Lock className="w-4 h-4 absolute left-3.5 top-3 text-slate-500" />
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="************"
                    required
                    className="w-full pl-10 pr-4 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition-colors"
                  />
                </div>
              </div>
            </>
          ) : (
            <div>
              <div className="p-3.5 mb-4 rounded-xl bg-indigo-500/15 border border-indigo-500/30 text-indigo-300 text-xs flex items-center gap-2">
                <ShieldCheck className="w-4 h-4 text-indigo-400 flex-shrink-0" />
                <span>{t("auth.mfaRequired")}</span>
              </div>
              <label className="block font-semibold text-slate-300 mb-1.5">{t("auth.totpCodeLabel")}</label>
              <div className="relative">
                <KeyRound className="w-4 h-4 absolute left-3.5 top-3.5 text-slate-500" />
                <input
                  type="text"
                  value={mfaCode}
                  onChange={(e) => setMfaCode(e.target.value)}
                  placeholder="123456"
                  maxLength={6}
                  required
                  className="w-full pl-10 pr-4 py-3 rounded-xl bg-slate-900/90 border border-slate-800 text-white text-center tracking-widest text-lg font-mono focus:outline-none focus:border-indigo-500 transition-colors"
                />
              </div>
            </div>
          )}

          <Button
            type="submit"
            variant="primary"
            size="lg"
            className="w-full mt-2"
            disabled={loading}
          >
            {loading ? t("common.loading") : challengeToken ? t("auth.verifyCodeButton") : t("auth.signInButton")}
          </Button>
        </form>

        <div className="mt-6 pt-4 border-t border-slate-800/80 text-center">
          <Link href="/" className="text-xs text-slate-400 hover:text-slate-200 transition-colors inline-flex items-center gap-1.5">
            <ArrowLeft className="w-3.5 h-3.5" />
            <span>Back to surface selection</span>
          </Link>
        </div>
      </Card>
    </div>
  );
}
